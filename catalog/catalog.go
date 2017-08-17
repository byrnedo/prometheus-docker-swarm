package catalog

import (
	"sync"
	log "github.com/sirupsen/logrus"
	"github.com/byrnedo/prometheus-docker-swarm/utils"
	"github.com/byrnedo/prometheus-docker-swarm/dockerwatcher"
	"github.com/byrnedo/prometheus-docker-swarm/plugins"
)

var (
	servicesMap *utils.ServiceMap
)

func init(){
	servicesMap = utils.NewServiceMap()
}


func StartCatalog(conf *utils.ConfigContext, wg *sync.WaitGroup, watchEvents <-chan dockerwatcher.Event) {
	for {
		select {
		case evt := <-watchEvents:
			if p := evt.GetClobberPayload(); p != nil {
				handleClobber(p.CopyData(), conf)
			} else if p := evt.GetCreatePayload(); p != nil {
				handleCreate(p, conf)
			} else if p := evt.GetRemovePayload(); p != nil {
				handleRemove(p, conf)
			} else {
				log.Error("catalog: unhandled message:", evt.Action)
			}
		}
	}
	log.Info("catalog: exitting")
	wg.Done()
}

func handleClobber(newData map[string][]utils.ServiceEndpoint, conf *utils.ConfigContext) {

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
		// notify plugins
		plugins.NotifyCatalogChange(servicesMap.Copy(), conf)
	}

}

func handleCreate(sE *utils.ServiceEndpoint, conf *utils.ConfigContext) {

	servicesMap.Append(sE.ServiceName, *sE)
	log.WithFields(log.Fields{"serviceName": sE.ServiceName, "serviceID": sE.ServiceID, "taskID": sE.TaskID, "ip": sE.Ip, "port": sE.Port}).Infoln("registering endpoint")

	// notify plugins
	plugins.NotifyCatalogChange(servicesMap.Copy(), conf)
	//q.Pub(servicesMap.Copy(), channels.ChannelCatalogChange)

}
func handleRemove(sE *utils.ServiceEndpoint, conf *utils.ConfigContext) {
	// remove
	if servicesMap.RemoveEndpoint(sE.ServiceName, sE.TaskID) {
		log.WithFields(log.Fields{"serviceName": sE.ServiceName, "taskID": sE.TaskID}).Infoln("deregistering endpoint")
		// notify plugins
		plugins.NotifyCatalogChange(servicesMap.Copy(), conf)
		//q.Pub(servicesMap.Copy(), channels.ChannelCatalogChange)
	} else {
		log.WithFields(log.Fields{"serviceName": sE.ServiceName, "taskID": sE.TaskID}).Debugln("no need to deregistering endpoint, not registered")
	}
}
