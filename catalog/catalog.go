package catalog

import (
	"sync"
	"github.com/docker/docker/client"
	"github.com/cskr/pubsub"
	log "github.com/sirupsen/logrus"
	"github.com/byrnedo/prometheus-docker-swarm/utils"
	"github.com/byrnedo/prometheus-docker-swarm/channels"
)

var (
	servicesMap *utils.ServiceMap
)

func init(){
	servicesMap = utils.NewServiceMap()
}


func StartCatalog(cli *client.Client, conf *utils.ConfigContext, wg *sync.WaitGroup, q *pubsub.PubSub) {
	ch := q.Sub(channels.ChannelEndpointCreate, channels.ChannelEndpointRemove, channels.ChannelCatalogClobber)
	for {
		select {
		case evt := <-ch:
			switch evt.(type) {
			case *utils.ServiceMap:
				newData := evt.(*utils.ServiceMap)
				handleClobber(newData.CopyData(), q)
			case utils.ServiceEndpoint:
				sE := evt.(utils.ServiceEndpoint)
				handleCreateRemove(sE, q)
			default:
				log.Error("catalog: unhandled message:", evt)
			}
		}
	}
	log.Info("catalog: exitting")
	wg.Done()
}

func handleClobber(newData map[string][]utils.ServiceEndpoint, q *pubsub.PubSub) {

	toCreate := make(map[string]utils.ServiceEndpoint)
	toRemove := make(map[string]utils.ServiceEndpoint)


	// TODO - I think I need lock on the serivce map for all of this
	oldData := servicesMap.CopyData()
	for k, endpoints := range oldData {
		for _, endpoint := range endpoints {
			toRemove[k + endpoint.TaskID] = endpoint
		}
	}

	servicesMap.SetData(newData)
	for k, endpoints := range newData {
		for _, endpoint := range endpoints {
			if _, f := toRemove[k + endpoint.TaskID]; f {
				delete(toRemove, k + endpoint.TaskID)
			} else {
				toCreate[k + endpoint.TaskID] = endpoint
			}
		}
	}

	changed := false
	for _, endpoint := range toRemove {
		log.WithFields(log.Fields{"serviceName": endpoint.ServiceName, "taskID": endpoint.TaskID}).Infoln("deregistering endpoint")
		changed = true
	}

	for _, endpoint := range toCreate {
		log.WithFields(log.Fields{"serviceName": endpoint.ServiceName, "serviceID": endpoint.ServiceID, "taskID": endpoint.TaskID, "ip": endpoint.Ip, "port": endpoint.Port}).Infoln("registering endpoint")
		changed = true
	}

	if changed {
		q.Pub(servicesMap, channels.ChannelCatalogChange)
	}

}

func handleCreateRemove(sE utils.ServiceEndpoint, q *pubsub.PubSub){

	if sE.Ip != "" {
		// add
		servicesMap.Append(sE.ServiceName, sE)
		log.WithFields(log.Fields{"serviceName": sE.ServiceName, "serviceID": sE.ServiceID, "taskID": sE.TaskID, "ip": sE.Ip, "port": sE.Port}).Infoln("registering endpoint")

		q.Pub(servicesMap, channels.ChannelCatalogChange)
	} else {
		// remove
		if servicesMap.RemoveEndpoint(sE.ServiceName, sE.TaskID) {
			log.WithFields(log.Fields{"serviceName": sE.ServiceName, "taskID": sE.TaskID}).Infoln("deregistering endpoint")
			q.Pub(servicesMap, channels.ChannelCatalogChange)
		} else {
			log.WithFields(log.Fields{"serviceName": sE.ServiceName, "taskID": sE.TaskID}).Debugln("no need to deregistering endpoint, not registered")
		}
	}
}
