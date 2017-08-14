package main

import (
	"context"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"
	"sync"
	"io"
	"github.com/docker/docker/api/types/events"
	"github.com/cskr/pubsub"
)

const (
	targetsConfPath = "/etc/prometheus/targets-from-swarm.json"
	svcNameLabel = "com.docker.swarm.service.name"
	svcIDLabel = "com.docker.swarm.service.id"
	svcTaskIDLabel = "com.docker.swarm.task.id"

)


func syncSwarmTasks(cli *client.Client, q *pubsub.PubSub) error {


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
		syncTask(&task, cli, ctx, q)
	}

	return nil
}

func syncTask(task *swarm.Task, cli *client.Client, ctx context.Context, q *pubsub.PubSub) {

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

		q.Pub(ServiceEndpoint{
			ServiceID: task.ServiceID,
			ServiceName: svcName,
			TaskID: task.ID,
			Port: metricsPort,
			Ip: ip,
		}, channelEndpointCreate)

	} else {

		log.WithFields(log.Fields{"serviceName": svcName, "serviceID": task.ServiceID, "taskID": task.ID}).Debugln("no network attachment")
	}

}

func syncTargetsOnce(cli *client.Client, conf *ConfigContext, q *pubsub.PubSub) (error) {

	return syncSwarmTasks(cli, q)

}

// TODO take a channel to push updates onto
func watchEvents(cli *client.Client, conf *ConfigContext, q *pubsub.PubSub) {

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

					syncTask(&task, cli, ctx, q)
					//doneGoneChanged = true


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

						syncTask(&task, cli, ctx, q)
						//doneGoneChanged = true
					default:
						svcId := m.Actor.Attributes[svcIDLabel]
						q.Pub(ServiceEndpoint{
							ServiceID: svcId,
							ServiceName: svcName,
							TaskID: taskId,
						}, channelEndpointRemove)
						//if initialServices.Has(svcName) {
						//	if initialServices.RemoveEndpoint(svcName, taskId) {
						//		log.WithFields(log.Fields{"serviceName": svcName, "taskID": taskId}).Infoln("deregistering endpoint")
						//		doneGoneChanged = true
						//	}
						//}
					}
				case "stop", "kill":
					taskId := m.Actor.Attributes[svcTaskIDLabel]
					svcName, ok := m.Actor.Attributes[svcNameLabel]
					if !ok {
						log.WithFields(log.Fields{"serviceName": svcName, "taskID": taskId}).Debugln("no service name label, not deregistering")
						continue
					}
					svcId := m.Actor.Attributes[svcIDLabel]
					q.Pub(ServiceEndpoint{
						ServiceID: svcId,
						ServiceName: svcName,
						TaskID: taskId,
					}, channelEndpointRemove)
					//if initialServices.Has(svcName) {
					//	if initialServices.RemoveEndpoint(svcName, taskId) {
					//		log.WithFields(log.Fields{"serviceName": svcName, "taskID": taskId}).Infoln("deregistering endpoint")
					//		doneGoneChanged = true
					//	}
					//}
					// delete specific task from services
				}

				//if doneGoneChanged {
				//	var err error
				//	prevHash, err = writeTargetsFile(initialServices, prevHash)
				//	if err != nil {
				//		log.Errorln("watchEvents: error writing targets:", err)
				//	}
				//}
			}
		}
	}
}

// TODO create a master services channel that will be source of truth and pass to watchEvents
// Let that channel select do the file write
func startTaskWatcher(cli *client.Client, conf *ConfigContext, wg *sync.WaitGroup, q *pubsub.PubSub) {
	defer wg.Done()

	log.Infoln("syncPromTargetsTask: starting")

	err := syncTargetsOnce(cli, conf, q)
	if err != nil {
		log.Errorln("syncPromTargetsTask: error:", err)
	}
	// TODO spin off a  sync targets periodically for consistency in case we screw up the eventsx
	// start watch
	watchEvents(cli, conf, q)
}
