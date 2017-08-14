package main

import (
	"sync"
	"github.com/docker/docker/client"
	"github.com/cskr/pubsub"
	log "github.com/sirupsen/logrus"
)

var (
	servicesMap *serviceMap
)

func init(){
	servicesMap = NewServiceMap()
}


func startCatalog(cli *client.Client, conf *ConfigContext, wg *sync.WaitGroup, q *pubsub.PubSub) {
	ch := q.Sub(channelEndpointCreate, channelEndpointRemove)
	for {
		select {
		case evt := <-ch:
			switch evt {
			case evt.(ServiceEndpoint):
				sE := evt.(ServiceEndpoint)
				if sE.Ip != "" {
					// add
					servicesMap.Append(sE.ServiceName, sE)
					log.WithFields(log.Fields{"serviceName": sE.ServiceName, "serviceID": sE.ServiceID, "taskID": sE.TaskID, "ip": sE.Ip, "port": sE.Port}).Infoln("registering endpoint")

					q.Pub(servicesMap.Copy(), channelCatalogChange)
				} else {
					// remove
					if servicesMap.RemoveEndpoint(sE.ServiceName, sE.TaskID) {
						log.WithFields(log.Fields{"serviceName": sE.ServiceName, "taskID": sE.TaskID}).Infoln("deregistering endpoint")
						q.Pub(servicesMap.Copy(), channelCatalogChange)
					}
				}

			}
		}
	}
	wg.Done()
}
