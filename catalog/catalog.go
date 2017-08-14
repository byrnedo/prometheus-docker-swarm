package catalog

import (
	"sync"
	"github.com/docker/docker/client"
	"github.com/cskr/pubsub"
	log "github.com/sirupsen/logrus"
	"github.com/byrnedo/prometheus-docker-swarm/dockerwatcher"
	"github.com/byrnedo/prometheus-docker-swarm/utils"
	"github.com/byrnedo/prometheus-docker-swarm/channels"
)

var (
	servicesMap *ServiceMap
)

func init(){
	servicesMap = NewServiceMap()
}


func StartCatalog(cli *client.Client, conf *utils.ConfigContext, wg *sync.WaitGroup, q *pubsub.PubSub) {
	ch := q.Sub(channels.ChannelEndpointCreate, channels.ChannelEndpointRemove)
	for {
		select {
		case evt := <-ch:
			switch evt {
			case evt.(dockerwatcher.ServiceEndpoint):
				sE := evt.(dockerwatcher.ServiceEndpoint)
				if sE.Ip != "" {
					// add
					servicesMap.Append(sE.ServiceName, sE)
					log.WithFields(log.Fields{"serviceName": sE.ServiceName, "serviceID": sE.ServiceID, "taskID": sE.TaskID, "ip": sE.Ip, "port": sE.Port}).Infoln("registering endpoint")

					q.Pub(servicesMap.Copy(), channels.ChannelCatalogChange)
				} else {
					// remove
					if servicesMap.RemoveEndpoint(sE.ServiceName, sE.TaskID) {
						log.WithFields(log.Fields{"serviceName": sE.ServiceName, "taskID": sE.TaskID}).Infoln("deregistering endpoint")
						q.Pub(servicesMap.Copy(), channels.ChannelCatalogChange)
					}
				}

			}
		}
	}
	wg.Done()
}
