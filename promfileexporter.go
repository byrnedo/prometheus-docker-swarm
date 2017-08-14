package main

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
)


func startPromExporter(cli *client.Client, conf *ConfigContext, wg *sync.WaitGroup, q *pubsub.PubSub) {
	ch := q.Sub(channelCatalogChange)
	prevHash := ""
	for {
		select {
		case evt := <-ch:
			switch evt {
			case evt.(*serviceMap):
				catalog := evt.(*serviceMap)

				if newHash, err := writeTargetsFile(catalog, prevHash); err != nil {
					log.Errorln("writeTargetsFile:", err)
				}else {
					prevHash = newHash
				}

			}
		}
	}
	wg.Done()
}

func writeTargetsFile(serviceAddresses *serviceMap, previousHash string) (string, error) {
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
		log.Debugf("writeTargetsFile: changed, writing to %s", targetsConfPath)

		if err := ioutil.WriteFile(targetsConfPath, promServiceTargetsFileContentJson, 0755); err != nil {
			log.Errorln("writeTargetsFile: error:", err)
		}
	} else {
		log.Debugln("writeTargetsFile: no changes")
	}

	return newHash, nil
}

