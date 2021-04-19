package utils

import "sync"

type Status int

const (
	UnknownStat Status = iota
	SuccessStat
	FailedStat
	DoingStat
)

type NotFoundErr int

func NewNotFoundErr() NotFoundErr {
	return NotFoundErr(0)
}

func (n NotFoundErr) Error() string {
	return "not found"
}

type HadAddErr int

func NewHadAddErrErr() HadAddErr {
	return HadAddErr(0)
}

func (n HadAddErr) Error() string {
	return "had add"
}

type Itemr interface {
	//search by id
	ID() int64

	//autually do
	Do() error
}

type item struct {
	Itemr
	stat   Status
	reason error
}

type Pending struct {
	mu    sync.RWMutex
	items map[int64]*item
}

func NewPending() *Pending {
	return &Pending{items: make(map[int64]*item)}
}

func (p *Pending) Status(id int64) (Status, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.items[id]
	if ok {
		return v.stat, v.reason
	}
	return UnknownStat, NewNotFoundErr()
}

func (p *Pending) Del(e Itemr) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.items, e.ID())
}

func (p *Pending) Add(e Itemr) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.items[e.ID()]
	if ok {
		return NewHadAddErrErr()
	}
	p.items[e.ID()] = &item{
		Itemr:  e,
		stat:   UnknownStat,
		reason: nil,
	}
	return nil
}
