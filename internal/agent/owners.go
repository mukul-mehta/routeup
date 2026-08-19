package agent

import "sync"

type routeOwnerControl struct {
	ownerPID int
	stop     chan struct{}
	ack      chan struct{}
	stopOnce sync.Once
	ackOnce  sync.Once
}

type routeOwnerControls struct {
	mu      sync.Mutex
	byRoute map[string]*routeOwnerControl
}

func newRouteOwnerControls() *routeOwnerControls {
	return &routeOwnerControls{byRoute: make(map[string]*routeOwnerControl)}
}

func (c *routeOwnerControls) attach(route string, ownerPID int) (*routeOwnerControl, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.byRoute[route]; exists {
		return nil, false
	}
	control := &routeOwnerControl{ownerPID: ownerPID, stop: make(chan struct{}), ack: make(chan struct{})}
	c.byRoute[route] = control
	return control, true
}

func (c *routeOwnerControls) detach(route string, control *routeOwnerControl) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byRoute[route] == control {
		delete(c.byRoute, route)
	}
}

func (c *routeOwnerControls) stop(route string, ownerPID int) *routeOwnerControl {
	c.mu.Lock()
	control := c.byRoute[route]
	c.mu.Unlock()
	if control == nil || control.ownerPID != ownerPID {
		return nil
	}
	control.stopOnce.Do(func() { close(control.stop) })
	return control
}

func (c *routeOwnerControls) acknowledge(route string, ownerPID int) bool {
	c.mu.Lock()
	control := c.byRoute[route]
	c.mu.Unlock()
	if control == nil || control.ownerPID != ownerPID {
		return false
	}
	control.ackOnce.Do(func() { close(control.ack) })
	return true
}
