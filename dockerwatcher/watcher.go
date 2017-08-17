package dockerwatcher

import (
	"context"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"
	"sync"
	"io"
	"github.com/docker/docker/api/types/events"
	"github.com/byrnedo/prometheus-docker-swarm/utils"
	"time"
)

const (
	svcNameLabel = "com.docker.swarm.service.name"
	svcIDLabel = "com.docker.swarm.service.id"
	svcTaskIDLabel = "com.docker.swarm.task.id"

	ActionClobber = "clobber"
	ActionCreate = "create"
	ActionRemove = "remove"

)

type Event struct {
	Action string
	Payload interface{}
}

func (this *Event) GetClobberPayload() *utils.ServiceMap {
	if this.Action != ActionClobber {
		return nil
	}
	d := this.Payload.(utils.ServiceMap)
	return &d
}

func (this *Event) GetCreatePayload() *utils.ServiceEndpoint {
	if this.Action != ActionCreate {
		return nil
	}
	d := this.Payload.(utils.ServiceEndpoint)
	return &d
}

func (this *Event) GetRemovePayload() *utils.ServiceEndpoint {
	if this.Action != ActionRemove {
		return nil
	}
	d := this.Payload.(utils.ServiceEndpoint)
	return &d
}


func syncTask(task *swarm.Task, cli *client.Client, ctx context.Context, evtChan chan <- Event) {

	hasMetricsEndpoint, metricsPort, _ := utils.ParseMetricsEndpointEnv(task.Spec.ContainerSpec.Env)
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
		ip := utils.ExtractIpFromNetmask(task.NetworksAttachments[0].Addresses[0])

		evtChan <- Event{
			Action: ActionCreate,
			Payload: utils.ServiceEndpoint{
				ServiceID: task.ServiceID,
				ServiceName: svcName,
				TaskID: task.ID,
				Port: metricsPort,
				Ip: ip,
			},
		}

	} else {

		log.WithFields(log.Fields{"serviceName": svcName, "serviceID": task.ServiceID, "taskID": task.ID}).Debugln("no network attachment")
	}

}

func clobberTargets(cli *client.Client, conf *utils.ConfigContext, evtChan chan <- Event) (error) {

	ctx := context.Background()
	// list tasks
	tasks, err := cli.TaskList(ctx, types.TaskListOptions{})
	if err != nil {
		return err
	}

	toClobber := utils.NewServiceMap()

	for _, task := range tasks {
		// TODO: this filter could probably be done with the TaskList() call more efficiently?

		if task.Status.State != swarm.TaskStateRunning {
			log.WithField("taskID", task.ID).Debugln("ignoring task since not running: ", task.Status.State)
			continue
		}

		hasMetricsEndpoint, metricsPort, _ := utils.ParseMetricsEndpointEnv(task.Spec.ContainerSpec.Env)
		if !hasMetricsEndpoint {
			log.WithFields(log.Fields{"serviceID": task.ServiceID, "taskID": task.ID}).Debugln("task has no metrics endpoint")
			continue
		}

		svc, _, err := cli.ServiceInspectWithRaw(ctx, task.ServiceID, types.ServiceInspectOptions{})
		if err != nil {
			log.WithFields(log.Fields{"serviceID": task.ServiceID, "taskID": task.ID}).Errorln("failed to inspect service:", err)
		}
		svcName := svc.Spec.Name

		if len(task.NetworksAttachments) > 0 && len(task.NetworksAttachments[0].Addresses) > 0 {
			ip := utils.ExtractIpFromNetmask(task.NetworksAttachments[0].Addresses[0])

			toClobber.Append(svcName, utils.ServiceEndpoint{
				ServiceID: task.ServiceID,
				ServiceName: svcName,
				TaskID: task.ID,
				Port: metricsPort,
				Ip: ip,
			})

		} else {

			log.WithFields(log.Fields{"serviceName": svcName, "serviceID": task.ServiceID, "taskID": task.ID}).Debugln("no network attachment")
		}
	}

	if toClobber.Len() > 0{
		log.Debugln("publishing clobber")
		evtChan <- Event {
			Action: ActionClobber,
			Payload: *toClobber,
		}
	} else {

		log.Debugln("not publishing clobber")
	}

	return nil

}

func watchEvents(cli *client.Client, conf *utils.ConfigContext, evtChan chan <- Event) {

	var (
		ctx = context.Background()
	)

	resync := time.NewTicker(conf.ResyncInterval).C

	log.Infoln("resyncing all endpoints")
	err := clobberTargets(cli, conf, evtChan)
	if err != nil {
		log.Errorln("clobberTargets:", err)
	}

	msgs, errs := cli.Events(ctx, types.EventsOptions{})
	for {
		select {
		case e := <-errs:
			if e == io.EOF {
				return
			} else {
				log.Errorln("watchEvents: error:", e)
			}
		case m := <-msgs:
			if m.Type == events.ContainerEventType {
				switch (m.Action) {
				case  "start":
					taskId := m.Actor.Attributes[svcTaskIDLabel]
					svcName, ok := m.Actor.Attributes[svcNameLabel]
					if !ok {
						continue
					}

					task, _, err := cli.TaskInspectWithRaw(ctx, taskId)
					if err != nil {
						log.Errorln("watchEvents: error inspecting task:", err)
						continue
					}

					if task.Spec.ContainerSpec.Healthcheck != nil {
						log.WithFields(log.Fields{"serviceName": svcName, "taskID": taskId}).Debugln("service has heathcheck, ignoring start event")
						continue
					}

					syncTask(&task, cli, ctx, evtChan)

				case "health_status":
					taskId := m.Actor.Attributes[svcTaskIDLabel]
					svcName, ok := m.Actor.Attributes[svcNameLabel]
					if !ok {
						log.WithFields(log.Fields{"serviceName": svcName, "taskID": taskId}).Debugln("no service name label, not deregistering")
						continue
					}

					switch m.Status {
					case "healthy":
						task, _, err := cli.TaskInspectWithRaw(ctx, taskId)
						if err != nil {
							log.Errorln("watchEvents: error inspecting task:", err)
							continue
						}
						syncTask(&task, cli, ctx, evtChan)
					default:
						svcId := m.Actor.Attributes[svcIDLabel]
						evtChan <- Event{
							Action: ActionRemove,
							Payload: utils.ServiceEndpoint{
								ServiceID: svcId,
								ServiceName: svcName,
								TaskID: taskId,
							},
						}
					}
				case "stop", "kill":
					taskId := m.Actor.Attributes[svcTaskIDLabel]
					svcName, ok := m.Actor.Attributes[svcNameLabel]
					if !ok {
						log.WithFields(log.Fields{"serviceName": svcName, "taskID": taskId}).Debugln("no service name label, not deregistering")
						continue
					}
					svcId := m.Actor.Attributes[svcIDLabel]
					evtChan <- Event{
						Action: ActionRemove,
						Payload: utils.ServiceEndpoint{
							ServiceID: svcId,
							ServiceName: svcName,
							TaskID: taskId,
						},
					}
				}
			}
		case <-resync:
			log.Infoln("resyncing all endpoints")
			err := clobberTargets(cli, conf, evtChan)
			if err != nil {
				log.Errorln("clobberTargets:", err)
			}
		}
	}
}

func StartWatcher(cli *client.Client, conf *utils.ConfigContext, wg *sync.WaitGroup, evtChan chan <- Event) {
	defer wg.Done()
	log.Infoln("startWatcher: starting events watcher")
	watchEvents(cli, conf, evtChan)
	log.Info("watcher: exitting")
}
