package main

import "sync"

type ServiceEndpoint struct {
	ServiceID string
	ServiceName string
	TaskID string
	Ip string
	Port int
}

func (this *ServiceEndpoint) Copy() ServiceEndpoint {
	return ServiceEndpoint{
		ServiceName: this.ServiceName,
		TaskID: this.TaskID,
		Ip: this.Ip,
		Port: this.Port,
	}
}

type serviceMap struct {
	Mutex *sync.RWMutex
	Data map[string][]ServiceEndpoint
}

func NewServiceMap() *serviceMap {
	return &serviceMap{
		Mutex: &sync.RWMutex{},
		Data: make(map[string][]ServiceEndpoint),
	}
}

func (this *serviceMap) Clear() {
	this.Mutex.Lock()
	defer this.Mutex.Unlock()
	this.Data = make(map[string][]ServiceEndpoint)
}

func (this *serviceMap) Set(key string, val []ServiceEndpoint) {
	this.Mutex.Lock()
	defer this.Mutex.Unlock()
	this.Data[key] = val
}

func (this *serviceMap) RemoveEndpoint(key, taskId string) bool {
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

func (this *serviceMap) Append(key string, val ServiceEndpoint) {
	this.Mutex.Lock()
	defer this.Mutex.Unlock()
	this.Data[key] = append(this.Data[key], val)
}

func (this *serviceMap) Has(key string) bool {
	this.Mutex.RLock()
	defer this.Mutex.RUnlock()
	_, has := this.Data[key]
	return has
}

func (this *serviceMap) Get(key string) []ServiceEndpoint {
	this.Mutex.RLock()
	defer this.Mutex.RUnlock()
	data := this.Data[key]
	var retD []ServiceEndpoint
	for _, e := range data {
		retD = append(retD, e.Copy())
	}
	return retD
}

func (this *serviceMap) Copy() *serviceMap {
	this.Mutex.RLock()
	defer this.Mutex.RUnlock()
	retD := NewServiceMap()
	for k, e := range this.Data {
		var endpointsC []ServiceEndpoint
		for _, endpoint := range e {
			endpointsC = append(endpointsC, endpoint.Copy())
		}
		retD.Set(k, endpointsC)
	}
	return retD
}
