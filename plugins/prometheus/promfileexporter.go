package prometheus

import (
	log "github.com/sirupsen/logrus"
	"strconv"
	"encoding/json"
	"fmt"
	"crypto/md5"
	"io/ioutil"
	"github.com/byrnedo/prometheus-docker-swarm/utils"
	"github.com/byrnedo/prometheus-docker-swarm/plugins"
)

type PromServiceTargetsList struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

type PromServiceTargetsFile []PromServiceTargetsList


type PrometheusPlugin struct {
	hash string
}

func (this *PrometheusPlugin) OnCatalogChange(endpoints *utils.ServiceMap, conf *utils.ConfigContext) {

	if newHash, err := writeTargetsFile(endpoints, conf, this.hash); err != nil {
		log.Errorln("writeTargetsFile:", err)
	}else {
		this.hash = newHash
	}

}

var (
	p *PrometheusPlugin
)

func init() {
	p = &PrometheusPlugin{}
	plugins.Register(p)
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

