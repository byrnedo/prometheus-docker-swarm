package main

import (
	dclient "github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"sync"
	"os"
	"errors"
	"strings"
	"github.com/cskr/pubsub"
	"github.com/byrnedo/prometheus-docker-swarm/dockerwatcher"
	"github.com/byrnedo/prometheus-docker-swarm/utils"
	"github.com/byrnedo/prometheus-docker-swarm/promexport"
	"github.com/byrnedo/prometheus-docker-swarm/catalog"
	"time"
)


// requires that this will be ran on a manager node
func main() {

	var (
		dockerHost string
		promTargetsPath string
		logLevel string
		hardResyncInterval time.Duration
		evtSubs uint
	)
	app := cli.NewApp()
	app.Name = "prometheus-docker-swarm"
	app.Usage = "Expose prometheus targets from docker swarm mode api"



	app.Flags = []cli.Flag {
		cli.StringFlag{
			Name:        "docker-host",
			Value:       "unix:///var/run/docker.sock",
			Usage:       "docker host string to connect with",
			Destination: &dockerHost,
		},
		cli.DurationFlag{
			Name:        "resync-period",
			Value: 	     30 * time.Second,
			Usage:       "interval on which to hard resync the service catalog",
			Destination: &hardResyncInterval,
		},
		cli.StringFlag{
			Name:        "targets-conf-path",
			Value: 	     "/etc/prometheus/targets-from-swarm.json",
			Usage:       "path to prometheus targets file",
			Destination: &promTargetsPath,
		},

		cli.StringFlag{
			Name:        "log-level",
			Value:       "info",
			Usage:       "log level",
			Destination: &logLevel,
		},
		cli.UintFlag{
			Name:        "subscribers",
			Value:       200,
			Usage:       "max number of subscribers to events",
			Destination: &evtSubs,
		},
	}

	app.Action = func(c *cli.Context) error {

		lvl := log.InfoLevel
		switch strings.ToLower(logLevel) {
		case "debug":
			lvl = log.DebugLevel
		case "info":
			lvl = log.InfoLevel
		case "warn":
			lvl = log.WarnLevel
		case "error":
			lvl = log.ErrorLevel
		case "fatal":
			lvl = log.FatalLevel
		default:
			return errors.New("invalid log level")
		}

		log.SetLevel(lvl)
		// connection errors are not actually handled here, but instead when we call our first method.
		client, err := dclient.NewClient(dockerHost, "", nil, nil)
		if err != nil {
			panic(err)
		}

		hostIp, err := utils.ResolveSelfSwarmIp(client)
		if err != nil {
			panic(err)
		}

		log.Infof("main: resolved host IP to %s", hostIp)

		conf := &utils.ConfigContext{
			HostIp: hostIp,
			TargetsConfPath: promTargetsPath,
			ResyncInterval: hardResyncInterval,
		}

		wg := &sync.WaitGroup{}


		// + 1 since we sub to it

		q := pubsub.New(100)

		wg.Add(1)

		go dockerwatcher.StartWatcher(client, conf, wg, q)

		wg.Add(1)

		go catalog.StartCatalog(client, conf, wg, q)

		wg.Add(1)

		go promexport.StartPromExporter(client, conf, wg, q)


		wg.Wait()

		return nil
	}

	if err := app.Run(os.Args); err != nil {
		panic(err)
	}
}
