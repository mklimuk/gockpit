package gockpit

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
)

type StateMutation struct {
	state    *State
	mutation *State
	dirty    bool
}

func (s *StateMutation) Set(key string, val interface{}) *StateMutation {
	// if nothing changes the mutation remains empty
	if s.state.data[key] == val {
		return s
	}
	s.dirty = true
	s.mutation.set(key, val)
	return s
}

func (s *StateMutation) SetError(key string, err error) *StateMutation {
	if s.state.errors == nil {
		s.state.errors = make(Errors)
	}
	if err == s.state.errors[key].Err {
		return s
	}
	s.dirty = true
	s.mutation.setError(key, err)
	return s
}

func (s *StateMutation) Apply() {
	s.state.apply(s.mutation)
}

type State struct {
	mx     sync.RWMutex
	data   map[string]interface{}
	errors Errors
	alerts Alerts
}

func (s *State) With() *StateMutation {
	return &StateMutation{
		state:    s,
		mutation: &State{},
	}
}

func (s *State) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		State  map[string]interface{} `json:"state"`
		Errors Errors                 `json:"errors,omitempty"`
		Alerts Alerts                 `json:"alerts,omitempty"`
	}{s.data, s.errors, s.alerts})
}

// Apply copies another state into s. This relies on the assumption that state is extensible only and nothing gets deleted from it.
func (s *State) apply(other *State) {
	s.mx.Lock()
	defer s.mx.Unlock()
	if s.data == nil {
		s.data = make(map[string]interface{})
	}
	for key, val := range other.data {
		s.data[key] = val
	}
	for key, a := range s.alerts {
		a.update(s.data[key], a)
	}
}

func (s *State) set(key string, val interface{}) *State {
	s.mx.Lock()
	defer s.mx.Unlock()
	if s.data == nil {
		s.data = make(map[string]interface{})
	}
	s.data[key] = val
	return s
}

func (s *State) Int(name string) int {
	s.mx.RLock()
	defer s.mx.RUnlock()
	if s.data == nil {
		s.data = make(map[string]interface{})
	}
	val := s.data[name]
	if val == nil {
		return 0
	}
	switch i := val.(type) {
	case int:
		return i
	case int32:
		return int(i)
	case int8:
		return int(i)
	case int64:
		return int(i)
	default:
		panic(fmt.Errorf("%v is not of integer type", i))
	}
}

func (s *State) Float(name string) float64 {
	s.mx.RLock()
	defer s.mx.RUnlock()
	if s.data == nil {
		s.data = make(map[string]interface{})
	}
	val := s.data[name]
	if val == nil {
		return 0.0
	}
	switch i := val.(type) {
	case float32:
		return float64(i)
	case float64:
		return i
	default:
		panic(fmt.Errorf("%v is not of float type", i))
	}
}

func (s *State) Elem(name string) interface{} {
	s.mx.RLock()
	defer s.mx.RUnlock()
	if s.data == nil {
		s.data = make(map[string]interface{})
	}
	return s.data[name]
}

func (s *State) Bool(name string) bool {
	s.mx.RLock()
	defer s.mx.RUnlock()
	if s.data == nil {
		s.data = make(map[string]interface{})
	}
	val := s.data[name]
	if val == nil {
		return false
	}
	switch i := val.(type) {
	case bool:
		return i
	default:
		panic(fmt.Errorf("%v is not of boolean type", i))
	}
}

func (s *State) String(name string) string {
	s.mx.RLock()
	defer s.mx.RUnlock()
	if s.data == nil {
		s.data = make(map[string]interface{})
	}
	val := s.data[name]
	if val == nil {
		return ""
	}
	switch s := val.(type) {
	case string:
		return s
	case float64:
		return strconv.FormatFloat(s, 'g', 2, 64)
	case float32:
		return strconv.FormatFloat(float64(s), 'g', 2, 32)
	case int:
		return strconv.Itoa(s)
	case int64:
		return strconv.FormatInt(s, 10)
	case bool:
		return strconv.FormatBool(s)
	default:
		return fmt.Sprintf("%v", s)
	}
}

func (s *State) HasErrors() bool {
	return len(s.errors) > 0
}

func (s *State) Err(name string) error {
	s.mx.RLock()
	defer s.mx.RUnlock()
	if s.errors == nil {
		return nil
	}
	return s.errors[name]
}

func (s *State) setError(code string, err error) *State {
	s.mx.Lock()
	defer s.mx.Unlock()
	if s.errors == nil {
		s.errors = make(Errors)
	}
	if err == nil {
		// clear previous occurrence
		if _, found := s.errors[code]; found {
			delete(s.errors, code)
		}
		return s
	}
	s.errors.Collect(code, err)
	return s
}

func (s *State) clearError(code string) *State {
	s.mx.Lock()
	defer s.mx.Unlock()
	if s.errors == nil {
		s.errors = make(Errors)
	}
	if _, found := s.errors[code]; found {
		delete(s.errors, code)
	}
	return s
}

func (s *State) getError(code string) error {
	if err, found := s.errors[code]; found {
		return err
	}
	return nil
}
