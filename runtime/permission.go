package runtime

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
)

type PermissionRequest struct {
	ID        string
	SessionID string
	Action    string
	Path      string
	approved  chan bool
}

type PermissionService struct {
	bus       *EventBus
	mu        sync.Mutex
	pending   map[string]*PermissionRequest
	autoAllow bool
}

func NewPermissionService(bus *EventBus, autoAllow bool) *PermissionService {
	return &PermissionService{
		bus:       bus,
		pending:   make(map[string]*PermissionRequest),
		autoAllow: autoAllow,
	}
}

func (ps *PermissionService) Request(sessionID, action, path string) (bool, error) {
	req := &PermissionRequest{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Action:    action,
		Path:      path,
		approved:  make(chan bool, 1),
	}

	ps.mu.Lock()
	ps.pending[req.ID] = req
	ps.mu.Unlock()

	ps.bus.Publish(Event{
		Type:      EventPermissionRequested,
		SessionID: sessionID,
		Data:      req,
	})

	if ps.autoAllow {
		ps.Approve(req.ID)
	}

	approved, ok := <-req.approved
	if !ok {
		return false, fmt.Errorf("permission request cancelled")
	}
	return approved, nil
}

func (ps *PermissionService) Approve(requestID string) {
	ps.mu.Lock()
	req, ok := ps.pending[requestID]
	if ok {
		delete(ps.pending, requestID)
	}
	ps.mu.Unlock()
	if ok {
		req.approved <- true
		close(req.approved)
	}
}

func (ps *PermissionService) Reject(requestID string) {
	ps.mu.Lock()
	req, ok := ps.pending[requestID]
	if ok {
		delete(ps.pending, requestID)
	}
	ps.mu.Unlock()
	if ok {
		req.approved <- false
		close(req.approved)
	}
}
