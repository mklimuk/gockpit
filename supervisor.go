package gockpit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi"

	"github.com/rs/zerolog/log"
)

var defaultSamplingInterval = time.Second

type Probe interface {
	UpdateState(context.Context, *StateMutation)
}

type ProbeFunc func(context.Context, *StateMutation)

type Listener func(*State)

type Reader interface {
}

type Writer interface {
	Save(ctx context.Context, bucket, name string, fields map[string]interface{}, tags map[string]string) error
}

type ReadWriter interface {
	Reader
	Writer
}

type Metric struct {
	name       string
	interval   time.Duration
	lastUpdate time.Time
	probe      interface{}
}

func NewMetric(name string, interval time.Duration, probe interface{}) *Metric {
	switch t := probe.(type) {
	case Probe:
	case ProbeFunc:
	default:
		panic(fmt.Errorf("invalid metric probe of type %s; one of gockpit.Probe, gockpit.ProbeFunc is expected", t))
	}
	return &Metric{
		name:     name,
		probe:    probe,
		interval: interval,
	}
}

func (mg *Metric) updateState(ctx context.Context, now time.Time, mutation *StateMutation) {
	if !now.After(mg.lastUpdate.Add(mg.interval)) {
		return
	}
	switch p := mg.probe.(type) {
	case Probe:
		p.UpdateState(ctx, mutation)
	case ProbeFunc:
		// probe functions do not provide a possibility to copy errors
		// during sampling
		p(ctx, mutation)
	}
}

type Supervisor struct {
	mx               sync.Mutex
	metrics          map[string]*Metric
	state            *State
	listeners        []Listener
	store            ReadWriter
	name             string
	samplingInterval time.Duration
	cancel           func()
}

type SupervisorOption func(*Supervisor)

func WithStore(store ReadWriter) SupervisorOption {
	return func(supervisor *Supervisor) {
		supervisor.store = store
	}
}

func WithSamplingInterval(interval time.Duration) SupervisorOption {
	return func(supervisor *Supervisor) {
		supervisor.samplingInterval = interval
	}
}

func NewSupervisor(name string, opts ...SupervisorOption) *Supervisor {
	s := &Supervisor{
		name:    name,
		metrics: make(map[string]*Metric),
		state: &State{
			data: make(map[string]interface{}),
		},
	}
	for _, o := range opts {
		o(s)
	}
	if s.samplingInterval == 0 {
		s.samplingInterval = defaultSamplingInterval
	}
	return s
}

func (s *Supervisor) GetState() *State {
	return s.state
}

func (s *Supervisor) Errors() Errors {
	return s.state.errors
}

func (s *Supervisor) AddProbe(name string, interval time.Duration, p interface{}) {
	s.mx.Lock()
	defer s.mx.Unlock()
	s.metrics[name] = NewMetric(name, interval, p)
}

func (s *Supervisor) AddAlert(ID string, a *Alert) {
	s.mx.Lock()
	defer s.mx.Unlock()
	if s.state.alerts == nil {
		s.state.alerts = make(Alerts)
	}
	s.state.alerts[ID] = a
}

func (s *Supervisor) AddListener(l Listener) {
	s.mx.Lock()
	defer s.mx.Unlock()
	s.listeners = append(s.listeners, l)
}

func (s *Supervisor) Run(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(s.samplingInterval)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				s.mx.Lock()
				mutation := s.state.With()

				for _, mg := range s.metrics {
					if now.After(mg.lastUpdate.Add(mg.interval)) {
						mg.updateState(ctx, now, mutation)
						mg.lastUpdate = now
					} else {
						// copy previous error
						if err := s.state.getError(mg.name); err != nil {
							mutation.SetError(mg.name, err)
						}
					}
				}
				mutation.Apply()
				if mutation.dirty {
					for _, l := range s.listeners {
						l(s.state)
					}
				}
				// persist state no matter if it has changed (time series)
				if s.store != nil {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					s.state.mx.RLock()
					err := s.store.Save(ctx, "gockpit", s.name, s.state.data, nil)
					s.state.mx.RUnlock()
					cancel()
					if err != nil {
						log.Error().Err(err).Msg("could not save metrics state")
					}
				}
				s.mx.Unlock()
			case <-ctx.Done():
			}
		}
	}()
}

func (s *Supervisor) Stop() {
	if s.cancel == nil {
		return
	}
	s.cancel()
}

func (s *Supervisor) CollectError(code string, err error) error {
	s.mx.Lock()
	defer s.mx.Unlock()
	s.state.setError(code, err)
	return err
}

func (s *Supervisor) handlerState(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	_ = enc.Encode(s.state)
}

func (s *Supervisor) String(id string) string {
	return s.state.String(id)
}

func (s *Supervisor) HTTPHandler() http.Handler {
	r := chi.NewRouter()
	r.Get("/state", s.handlerState)
	return r
}
