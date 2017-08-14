package utils

import (
	"sync"
)

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


type ServiceMap struct {
	Mutex *sync.RWMutex
	Data map[string][]ServiceEndpoint
}

func NewServiceMap() *ServiceMap {
	return &ServiceMap{
		Mutex: &sync.RWMutex{},
		Data: make(map[string][]ServiceEndpoint),
	}
}

func (this *ServiceMap) SetData(data map[string][]ServiceEndpoint) {
	this.Mutex.Lock()
	defer this.Mutex.Unlock()
	this.Data = data
}

func (this *ServiceMap) CopyData()( map[string][]ServiceEndpoint) {
	this.Mutex.RLock()
	defer this.Mutex.RUnlock()

	retD := make(map[string][]ServiceEndpoint)
	for k, e := range this.Data {
		var endpointsC []ServiceEndpoint
		for _, endpoint := range e {
			endpointsC = append(endpointsC, endpoint.Copy())
		}
		retD[k] = endpointsC
	}
	return retD
}

func (this *ServiceMap) Clear() {
	this.Mutex.Lock()
	defer this.Mutex.Unlock()
	this.Data = make(map[string][]ServiceEndpoint)
}
func (this *ServiceMap) Len() int {
	this.Mutex.Lock()
	defer this.Mutex.Unlock()
	return len(this.Data)
}

func (this *ServiceMap) Set(key string, val []ServiceEndpoint) {
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

func (this *ServiceMap) Append(key string, val ServiceEndpoint) {
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

func (this *ServiceMap) Get(key string) []ServiceEndpoint {
	this.Mutex.RLock()
	defer this.Mutex.RUnlock()
	data := this.Data[key]
	var retD []ServiceEndpoint
	for _, e := range data {
		retD = append(retD, e.Copy())
	}
	return retD
}

func (this *ServiceMap) Copy() *ServiceMap {
	retD := NewServiceMap()
	retD.SetData(this.CopyData())
	return retD
}
