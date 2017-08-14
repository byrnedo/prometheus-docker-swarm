package catalog

import (
	"sync"
	"github.com/byrnedo/prometheus-docker-swarm/dockerwatcher"
)

type ServiceMap struct {
	Mutex *sync.RWMutex
	Data map[string][]dockerwatcher.ServiceEndpoint
}

func NewServiceMap() *ServiceMap {
	return &ServiceMap{
		Mutex: &sync.RWMutex{},
		Data: make(map[string][]dockerwatcher.ServiceEndpoint),
	}
}

func (this *ServiceMap) Clear() {
	this.Mutex.Lock()
	defer this.Mutex.Unlock()
	this.Data = make(map[string][]dockerwatcher.ServiceEndpoint)
}

func (this *ServiceMap) Set(key string, val []dockerwatcher.ServiceEndpoint) {
	this.Mutex.Lock()
	defer this.Mutex.Unlock()
	this.Data[key] = val
}

func (this *ServiceMap) RemoveEndpoint(key, taskId string) bool {
	this.Mutex.Lock()
	defer this.Mutex.Unlock()

	found := false
	for idx, e := range this.Data[key] {
		if e.TaskID == taskId {
			found = true
			this.Data[key] = append(this.Data[key][:idx], this.Data[key][idx + 1:]...)
		}
	}
	return found
}

func (this *ServiceMap) Append(key string, val dockerwatcher.ServiceEndpoint) {
	this.Mutex.Lock()
	defer this.Mutex.Unlock()
	this.Data[key] = append(this.Data[key], val)
}

func (this *ServiceMap) Has(key string) bool {
	this.Mutex.RLock()
	defer this.Mutex.RUnlock()
	_, has := this.Data[key]
	return has
}

func (this *ServiceMap) Get(key string) []dockerwatcher.ServiceEndpoint {
	this.Mutex.RLock()
	defer this.Mutex.RUnlock()
	data := this.Data[key]
	var retD []dockerwatcher.ServiceEndpoint
	for _, e := range data {
		retD = append(retD, e.Copy())
	}
	return retD
}

func (this *ServiceMap) Copy() *ServiceMap {
	this.Mutex.RLock()
	defer this.Mutex.RUnlock()
	retD := NewServiceMap()
	for k, e := range this.Data {
		var endpointsC []dockerwatcher.ServiceEndpoint
		for _, endpoint := range e {
			endpointsC = append(endpointsC, endpoint.Copy())
		}
		retD.Set(k, endpointsC)
	}
	return retD
}

