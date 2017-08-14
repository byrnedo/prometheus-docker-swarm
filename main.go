package main

import (
	dclient "github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"sync"
	"os"
	"errors"
)

// requires that this will be ran on a manager node
func main() {

	var (
		dockerHost string
		logLevel string
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

		cli.StringFlag{
			Name:        "log-level",
			Value:       "warn",
			Usage:       "log level",
			Destination: &logLevel,
		},
	}

	app.Action = func(c *cli.Context) error {

		lvl := log.InfoLevel
		switch logLevel {
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

		hostIp, err := resolveSelfSwarmIp(client)
		if err != nil {
			panic(err)
		}

		log.Infof("main: resolved host IP to %s", hostIp)

		conf := &ConfigContext{hostIp}

		wg := &sync.WaitGroup{}

		wg.Add(1)

		go syncPromTargetsTask(client, conf, wg)

		wg.Wait()

		return nil
	}

	if err := app.Run(os.Args); err != nil {
		panic(err)
	}
}
