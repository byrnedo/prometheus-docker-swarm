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

// host networking not supported in Docker Swarm, so we have to
// have specialized support for it
func syncHostNetworkedContainers(serviceAddresses map[string][]string, cli *client.Client, conf *ConfigContext) error {
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

			hostPort := fmt.Sprintf("%s:%d", conf.HostIp, endpointPort)

			serviceAddresses[serviceName] = append(serviceAddresses[serviceName], hostPort)
		} else {
			log.Printf("is not host networked container")
		}
	}

	return nil
}

func syncSwarmTasks(serviceAddresses map[string][]string, cli *client.Client) error {
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

			serviceAddresses[taskServiceName] = append(serviceAddresses[taskServiceName], ip+":"+strconv.Itoa(metricsPort))
		}
	}

	return nil
}

func writeTargetsFile(serviceAddresses map[string][]string, previousHash string) (string, error) {
	promServiceTargetsFileContent := PromServiceTargetsFile{}

	for serviceId, addresses := range serviceAddresses {
		labels := map[string]string{
			"job": serviceId,
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

func syncTargetsOnce(cli *client.Client, conf *ConfigContext) (string, map[string][]string, error) {
	serviceAddresses := make(map[string][]string)

	if err := syncHostNetworkedContainers(serviceAddresses, cli, conf); err != nil {
		return "", err
	}

	if err := syncSwarmTasks(serviceAddresses, cli); err != nil {
		return "", err
	}

	newHash, err := writeTargetsFile(serviceAddresses, "")

	return newHash, serviceAddresses, err
}

func watchEvents(cli *client.Client, conf *ConfigContext, prevHash string, initialServices map[string][]string) {

	var (
		done chan struct{}
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
		case m := <-msgs:
			if m.Type == events.ContainerEventType {
				switch (m.Action) {
				case "create":
					// new
				case "start":
					// new
				case "stop":
					// remove
				case "kill":
					// remove
				}
			}
		}
	}
}

func syncPromTargetsTask(cli *client.Client, conf *ConfigContext, wg *sync.WaitGroup) {
	defer wg.Done()

	log.Printf("syncPromTargetsTask: starting")

	newHash, startTasks, err := syncTargetsOnce(cli, conf)
	if err != nil {
		log.Printf("syncPromTargetsTask: error:", err)
	}
	// start watch
	watchEvents(cli, conf, newHash, startTasks)
}
