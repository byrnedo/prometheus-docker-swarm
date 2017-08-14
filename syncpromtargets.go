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
	log "github.com/sirupsen/logrus"
	"strconv"
	"sync"
	"io"
	"github.com/docker/docker/api/types/events"
)

const (
	targetsConfPath = "/etc/prometheus/targets-from-swarm.json"
	svcNameLabel = "com.docker.swarm.service.name"
	svcTaskIDLabel = "com.docker.swarm.task.id"

)


var (
	ServicesMap *serviceMap
)

func init(){
	ServicesMap = NewServiceMap()
}

func syncSwarmTasks(serviceAddresses *serviceMap, cli *client.Client) error {


	ctx := context.Background()
	// list tasks
	tasks, err := cli.TaskList(ctx, types.TaskListOptions{})
	if err != nil {
		return err
	}

	for _, task := range tasks {
		// TODO: this filter could probably be done with the TaskList() call more efficiently?

		if task.Status.State != swarm.TaskStateRunning {
			log.WithField("taskID", task.ID).Debugln("ignoring task since not running: ", task.Status.State)
			continue
		}
		syncTask(&task, serviceAddresses, cli, ctx)
	}

	return nil
}

func syncTask(task *swarm.Task, serviceAddresses *serviceMap, cli *client.Client, ctx context.Context) {

	hasMetricsEndpoint, metricsPort, _ := parseMetricsEndpointEnv(task.Spec.ContainerSpec.Env)
	if !hasMetricsEndpoint {
		log.WithFields(log.Fields{"serviceID": task.ServiceID, "taskID": task.ID}).Debugln("task has no metrics endpoint")
		return
	}

	svc, _, err := cli.ServiceInspectWithRaw(ctx, task.ServiceID, types.ServiceInspectOptions{})
	if err != nil {
		log.WithFields(log.Fields{"serviceID": task.ServiceID, "taskID": task.ID}).Errorln("failed to inspect service:", err)
	}
	svcName := svc.Spec.Name

	if len(task.NetworksAttachments) > 0 && len(task.NetworksAttachments[0].Addresses) > 0 {
		ip := extractIpFromNetmask(task.NetworksAttachments[0].Addresses[0])

		log.WithFields(log.Fields{"serviceName": svcName, "serviceID": task.ServiceID, "taskID": task.ID, "ip": ip, "port": metricsPort}).Infoln("registering endpoint")
		serviceAddresses.Append(svcName, ServiceEndpoint{
			TaskID: task.ID,
			Port: metricsPort,
			Ip: ip,
		})
	} else {

		log.WithFields(log.Fields{"serviceName": svcName, "serviceID": task.ServiceID, "taskID": task.ID}).Debugln("no network attachment")
	}

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

func syncTargetsOnce(cli *client.Client, conf *ConfigContext, serviceAddresses *serviceMap) (string, *serviceMap, error) {

	serviceAddresses.Clear()

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
					log.Errorln("watchEvents: error:", e)
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
				doneGoneChanged := false

				switch (m.Action) {
				case  "start":
					taskId := m.Actor.Attributes[svcTaskIDLabel]
					_, ok := m.Actor.Attributes[svcNameLabel]
					if !ok {
						continue
					}

					task, _, err := cli.TaskInspectWithRaw(ctx, taskId)
					if err != nil {
						log.Errorln("watchEvents: error inspecting task:", err)
						continue
					}

					if task.Spec.ContainerSpec.Healthcheck != nil {
						continue
					}

					syncTask(&task, initialServices, cli, ctx)
					doneGoneChanged = true


				case "health_status":
					taskId := m.Actor.Attributes[svcTaskIDLabel]
					svcName, ok := m.Actor.Attributes[svcNameLabel]
					if !ok {
						continue
					}

					switch m.Status {
					case "healthy":
						task, _, err := cli.TaskInspectWithRaw(ctx, taskId)
						if err != nil {
							log.Errorln("watchEvents: error inspecting task:", err)
							continue
						}

						syncTask(&task, initialServices, cli, ctx)
						doneGoneChanged = true
					default:
						if initialServices.Has(svcName) {
							if initialServices.RemoveEndpoint(svcName, taskId) {
								log.WithFields(log.Fields{"serviceName": svcName, "taskID": taskId}).Infoln("deregistering endpoint")
								doneGoneChanged = true
							}
						}
					}
				case "stop", "kill":
					taskId := m.Actor.Attributes[svcTaskIDLabel]
					svcName, ok := m.Actor.Attributes[svcNameLabel]
					if !ok {
						log.WithFields(log.Fields{"serviceName": svcName, "taskID": taskId}).Debugln("no service name label, not deregistering")
						continue
					}
					if initialServices.Has(svcName) {
						if initialServices.RemoveEndpoint(svcName, taskId) {
							log.WithFields(log.Fields{"serviceName": svcName, "taskID": taskId}).Infoln("deregistering endpoint")
							doneGoneChanged = true
						}
					}
					// delete specific task from services
				}

				if doneGoneChanged {
					var err error
					prevHash, err = writeTargetsFile(initialServices, prevHash)
					if err != nil {
						log.Errorln("watchEvents: error writing targets:", err)
					}
				}
			}
		}
	}
}

// TODO create a master services channel that will be source of truth and pass to watchEvents
// Let that channel select do the file write
func syncPromTargetsTask(cli *client.Client, conf *ConfigContext, wg *sync.WaitGroup) {
	defer wg.Done()

	log.Infoln("syncPromTargetsTask: starting")

	newHash, _, err := syncTargetsOnce(cli, conf, ServicesMap)
	if err != nil {
		log.Errorln("syncPromTargetsTask: error:", err)
	}
	// TODO spin off a  sync targets periodically for consistency in case we screw up the eventsx
	// start watch
	watchEvents(cli, conf, newHash, ServicesMap)
}
