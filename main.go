package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	cron "github.com/robfig/cron/v3"
	"gopkg.in/yaml.v2"
)

var configFile = flag.String("config", "config.yml", "Configuration File")

type GaugeConfig struct {
	MetricName string            `yaml:"metricName"`
	MqttName   string            `yaml:"mqttName"`
	Help       string            `yaml:"help"`
	Labels     map[string]string `yaml:"labels"`
}

type Config struct {
	Mqtt struct {
		Host     string `yaml:"host"`
		Port     uint16 `yaml:"port"`
		ClientID string `yaml:"clientID"`
	} `yaml:"mqtt"`
	Prom struct {
		Http struct {
			Addr string `yaml:"addr"`
		} `yaml:"http"`
		Gauges []GaugeConfig `yaml:"gauges"`
	} `yaml:"prom"`
}

func buildGauge(gaugeConfig GaugeConfig, mqttClient mqtt.Client) {
	labelNames := make([]string, 0, len(gaugeConfig.Labels))
	for k := range gaugeConfig.Labels {
		labelNames = append(labelNames, k)
	}

	promGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: gaugeConfig.MetricName,
			Help: gaugeConfig.Help,
		},
		labelNames,
	)

	var handler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
		fmt.Printf("%s: %s\n", msg.Topic(), msg.Payload())
		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		topicKeys := strings.Split(msg.Topic(), "/")
		labels := make(map[string]string)
		for key, val := range gaugeConfig.Labels {
			index := 0
			n, _ := fmt.Sscanf(val, "$%d", &index)
			if n == 1 {
				labels[key] = topicKeys[index]
			} else {
				labels[key] = val
			}
		}
		promGauge.With(prometheus.Labels(labels)).Set(f)
	}

	prometheus.MustRegister(promGauge)

	if token := mqttClient.Subscribe(gaugeConfig.MqttName, 0, handler); token.Wait() && token.Error() != nil {
		fmt.Println(token.Error())
		os.Exit(1)
	}
}

func main() {
	flag.Parse()

	yamlFile, err := ioutil.ReadFile(*configFile)
	if err != nil {
		log.Fatalf("unable to open '%s': %v", *configFile, err)
	}

	config := Config{}
	err = yaml.Unmarshal(yamlFile, &config)
	if err != nil {
		log.Fatalf("unable to parse '%s': %v", *configFile, err)
	}

	//mqtt.DEBUG = log.New(os.Stdout, "", 0)
	mqtt.WARN = log.New(os.Stdout, "", 0)
	opts := mqtt.NewClientOptions().AddBroker(fmt.Sprintf("tcp://%s:%d", config.Mqtt.Host, config.Mqtt.Port)).SetClientID(config.Mqtt.ClientID)
	opts.SetKeepAlive(10 * time.Second)
	opts.SetPingTimeout(5 * time.Second)

	mqttClient := mqtt.NewClient(opts)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	crony := cron.New()
	crony.AddFunc("* * * * *", func() {
		fmt.Println("publishig up time...")
		mqttClient.Publish("mqtt-prometheus-exporter/state/up", 0, false, fmt.Sprintf("%d", time.Now().Unix()))
	})
	crony.Start()

	for _, gaugeConfig := range config.Prom.Gauges {
		buildGauge(gaugeConfig, mqttClient)
	}

	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(config.Prom.Http.Addr, nil))
}
