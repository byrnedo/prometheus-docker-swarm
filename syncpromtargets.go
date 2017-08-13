package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"io/ioutil"
	"log"
	"strconv"
	"sync"
	"io"
	"github.com/docker/docker/api/types/events"
)

const targetsConfPath = "/etc/prometheus/targets-from-swarm.json"


var (
	ServicesMap *serviceMap
)

func init(){
	ServicesMap = NewServiceMap()
}

// host networking not supported in Docker Swarm, so we have to
// have specialized support for it
func syncHostNetworkedContainers(serviceAddresses *serviceMap, cli *client.Client, conf *ConfigContext) error {
	containerList, err := cli.ContainerList(context.Background(), types.ContainerListOptions{})
	if err != nil {
		return err
	}

	for _, container := range containerList {
		// skip over Swarm-managed tasks (they are scraped automatically)
		if _, isSwarmTask := container.Labels["com.docker.swarm.task.name"]; isSwarmTask {
			continue
		}

		// using labels because ENVs are not visible in ContainerList()
		metricsEndpointSpec, hasMetricsEndpoint := container.Labels["METRICS_ENDPOINT"]

		if !hasMetricsEndpoint {
			continue
		}

		endpointPort := parseMetricsEndpointSpec(metricsEndpointSpec)

		_, isHost := container.NetworkSettings.Networks["host"]
		if isHost && len(container.Names) > 0 && len(container.Names[0]) > 1 {
			// for some reason "$ docker run --name foo" yields "/foo"
			serviceName := container.Names[0][1:]
			serviceAddresses.Append(serviceName, ServiceEndpoint{
				TaskID: container.ID,
				Port: endpointPort,
				Ip: conf.HostIp,
			})
		} else {
			log.Printf("is not host networked container")
		}
	}

	return nil
}

func syncSwarmTasks(serviceAddresses *serviceMap, cli *client.Client) error {
	services, err := cli.ServiceList(context.Background(), types.ServiceListOptions{})
	if err != nil {
		return err
	}

	serviceById := map[string]swarm.Service{}

	for _, service := range services {
		serviceById[service.ID] = service
	}

	// list tasks
	tasks, err := cli.TaskList(context.Background(), types.TaskListOptions{})
	if err != nil {
		return err
	}

	for _, task := range tasks {
		// TODO: this filter could probably be done with the TaskList() call more efficiently?
		if task.Status.State != swarm.TaskStateRunning {
			continue
		}

		hasMetricsEndpoint, metricsPort, _ := parseMetricsEndpointEnv(task.Spec.ContainerSpec.Env)

		if !hasMetricsEndpoint {
			continue
		}

		if len(task.NetworksAttachments) > 0 && len(task.NetworksAttachments[0].Addresses) > 0 {
			ip := extractIpFromNetmask(task.NetworksAttachments[0].Addresses[0])

			taskServiceName := serviceById[task.ServiceID].Spec.Name

			serviceAddresses.Append(taskServiceName, ServiceEndpoint{
				TaskID: task.ID,
				Port: metricsPort,
				Ip: ip,
			})
		}
	}

	return nil
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
		log.Printf("writeTargetsFile: changed, writing to %s", targetsConfPath)

		if err := ioutil.WriteFile(targetsConfPath, promServiceTargetsFileContentJson, 0755); err != nil {
			log.Printf("writeTargetsFile: error:", err)
		}
	} else {
		log.Printf("writeTargetsFile: no changes")
	}

	return newHash, nil
}

func syncTargetsOnce(cli *client.Client, conf *ConfigContext, serviceAddresses *serviceMap) (string, *serviceMap, error) {

	serviceAddresses.Clear()

	if err := syncHostNetworkedContainers(serviceAddresses, cli, conf); err != nil {
		return "", nil, err
	}

	if err := syncSwarmTasks(serviceAddresses, cli); err != nil {
		return "", nil, err
	}

	newHash, err := writeTargetsFile(serviceAddresses, "")

	return newHash, serviceAddresses, err
}

// TODO take a channel to push updates onto
func watchEvents(cli *client.Client, conf *ConfigContext, prevHash string, initialServices *serviceMap) {

	var (
		done chan bool
		ctx = context.Background()
	)
	msgs, errs := cli.Events(ctx, types.EventsOptions{})
	go func() {
		for {
			select  {
			case e := <-errs:
				if e == io.EOF {
					done <- true
				} else {
					log.Printf("watchEvents: error: %s", e)
				}
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		case m := <-msgs:
			if m.Type == events.ContainerEventType {
				switch (m.Action) {
				case "create", "start":
					//name := m.Actor.Attributes["com.docker.swarm.service.name"]
					// Get task
					// Push onto services map
					var err error
					prevHash, err = writeTargetsFile(initialServices, prevHash)
					if err != nil {
						log.Printf("watchEvents: error writing targets: %s", err)
					}
				case "stop", "kill":
					svcName := m.Actor.Attributes["com.docker.swarm.service.name"]
					taskId := m.ID
					if initialServices.Has(svcName) {
						endpoints := initialServices.Get(svcName)
						for idx, e := range endpoints {
							if e.TaskID == taskId {
								endpoints = append(endpoints[:idx], endpoints[idx+1:]...)
								initialServices.Set(svcName, endpoints)
								break
							}
						}
					}
					var err error
					prevHash, err = writeTargetsFile(initialServices, prevHash)
					if err != nil {
						log.Printf("watchEvents: error writing targets: %s", err)
					}
					// delete specific task from services
				}
			}
		}
	}
}

// TODO create a master services channel that will be source of truth and pass to watchEvents
// Let that channel select do the file write
func syncPromTargetsTask(cli *client.Client, conf *ConfigContext, wg *sync.WaitGroup) {
	defer wg.Done()

	log.Printf("syncPromTargetsTask: starting")

	newHash, _, err := syncTargetsOnce(cli, conf, ServicesMap)
	if err != nil {
		log.Printf("syncPromTargetsTask: error:", err)
	}
	// TODO spin off a  sync targets periodically for consistency in case we screw up the eventsx
	// start watch
	watchEvents(cli, conf, newHash, ServicesMap)
}
