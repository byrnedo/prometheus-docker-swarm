package dockerwatcher


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

