package plugins

import "github.com/byrnedo/prometheus-docker-swarm/utils"


var (
	pluginsList []Plugin
)

func Register(p Plugin) error {
	pluginsList = append(pluginsList, p)
	return nil
}

type Plugin interface {
	OnCatalogChange(*utils.ServiceMap, *utils.ConfigContext)
}

func NotifyCatalogChange(catalog *utils.ServiceMap, conf *utils.ConfigContext) {
	for _, p := range pluginsList {
		p.OnCatalogChange(catalog.Copy(), conf)
	}
}