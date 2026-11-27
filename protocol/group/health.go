package group

import "sync"

// healthSource is a group whose health, as a group that holds it as a member
// sees it, can change between that group's own test rounds.
type healthSource interface {
	addHealthListener(listener func())
}

// healthListeners holds the functions to call when a group's health changed.
// The zero value is ready to use.
type healthListeners struct {
	access    sync.Mutex
	listeners []func()
}

func (l *healthListeners) add(listener func()) {
	l.access.Lock()
	defer l.access.Unlock()
	l.listeners = append(l.listeners, listener)
}

func (l *healthListeners) notify() {
	l.access.Lock()
	listeners := l.listeners
	l.access.Unlock()
	for _, listener := range listeners {
		listener()
	}
}
