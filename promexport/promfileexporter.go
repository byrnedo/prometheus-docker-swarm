package promexport

import (
	"sync"
	"github.com/docker/docker/client"
	"github.com/cskr/pubsub"
	log "github.com/sirupsen/logrus"
	"strconv"
	"encoding/json"
	"fmt"
	"crypto/md5"
	"io/ioutil"
	"github.com/byrnedo/prometheus-docker-swarm/utils"
	"github.com/byrnedo/prometheus-docker-swarm/channels"
)

type PromServiceTargetsList struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

type PromServiceTargetsFile []PromServiceTargetsList


func StartPromExporter(cli *client.Client, conf *utils.ConfigContext, wg *sync.WaitGroup, q *pubsub.PubSub) {
	ch := q.Sub(channels.ChannelCatalogChange)
	prevHash := ""
	for {
		select {
		case evt := <-ch:
			switch evt {
			case evt.(*utils.ServiceMap):
				clg := evt.(*utils.ServiceMap)

				if newHash, err := writeTargetsFile(clg, conf, prevHash); err != nil {
					log.Errorln("writeTargetsFile:", err)
				}else {
					prevHash = newHash
				}

			}
		}
	}
	log.Info("promexport: exitting")
	wg.Done()
}

func writeTargetsFile(serviceAddresses *utils.ServiceMap, conf *utils.ConfigContext, previousHash string) (string, error) {
	promServiceTargetsFileContent := PromServiceTargetsFile{}

	svcMapCopy := serviceAddresses.Copy()
	for serviceId, endpoints := range svcMapCopy.Data {
		labels := map[string]string{
			"job": serviceId,
		}

		var addresses []string
		for _, endpoint := range endpoints {
			addresses = append(addresses, endpoint.Ip + ":" + strconv.Itoa(endpoint.Port))
		}

		serviceTarget := PromServiceTargetsList{addresses, labels}

		promServiceTargetsFileContent = append(promServiceTargetsFileContent, serviceTarget)
	}

	promServiceTargetsFileContentJson, err := json.MarshalIndent(promServiceTargetsFileContent, "", "    ")
	if err != nil {
		return previousHash, err
	}

	newHash := fmt.Sprintf("%x", md5.Sum(promServiceTargetsFileContentJson))

	if newHash != previousHash {
		log.Debugf("writeTargetsFile: changed, writing to %s", conf.TargetsConfPath)

		if err := ioutil.WriteFile(conf.TargetsConfPath, promServiceTargetsFileContentJson, 0755); err != nil {
			log.Errorln("writeTargetsFile: error:", err)
		}
	} else {
		log.Debugln("writeTargetsFile: no changes")
	}

	return newHash, nil
}

