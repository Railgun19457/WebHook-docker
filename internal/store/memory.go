package store

import (
	"sort"
	"sync"

	"webhook-docker/internal/model"
)

type ExecutionStore interface {
	Save(record model.ExecutionRecord)
	Update(record model.ExecutionRecord)
	Get(requestID string) (model.ExecutionRecord, bool)
	List(limit int) []model.ExecutionRecord
}

type MemoryStore struct {
	mu      sync.RWMutex
	records map[string]model.ExecutionRecord
	order   []string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		records: make(map[string]model.ExecutionRecord),
		order:   make([]string, 0, 128),
	}
}

func (s *MemoryStore) Save(record model.ExecutionRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[record.RequestID]; !exists {
		s.order = append(s.order, record.RequestID)
	}
	s.records[record.RequestID] = record
}

func (s *MemoryStore) Update(record model.ExecutionRecord) {
	s.Save(record)
}

func (s *MemoryStore) Get(requestID string) (model.ExecutionRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[requestID]
	return record, ok
}

func (s *MemoryStore) List(limit int) []model.ExecutionRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > len(s.order) {
		limit = len(s.order)
	}
	if limit == 0 {
		return nil
	}

	start := len(s.order) - limit
	ids := append([]string(nil), s.order[start:]...)
	sort.SliceStable(ids, func(i, j int) bool {
		a := s.records[ids[i]]
		b := s.records[ids[j]]
		return a.StartedAt.Before(b.StartedAt)
	})

	result := make([]model.ExecutionRecord, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		result = append(result, s.records[ids[i]])
	}
	return result
}
