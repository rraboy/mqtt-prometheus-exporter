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
var brokerMetrics map[string]*prometheus.GaugeVec = make(map[string]*prometheus.GaugeVec)
var lastMetricsTick = time.Now()

var brokerMetricsIgnore = []string{
	"$SYS/broker/version",
	"$SYS/broker/uptime",
}

type GaugeConfig struct {
	MetricName string            `yaml:"metricName"`
	MqttName   string            `yaml:"mqttName"`
	Help       string            `yaml:"help"`
	Labels     map[string]string `yaml:"labels"`
}

type Config struct {
	Mqtt struct {
		URL       string `yaml:"url"`
		ClientID  string `yaml:"clientID"`
		ExposeSys bool   `yaml:"exposeSys"`
		Topics    struct {
			Uptime string `yaml:"uptime"`
		} `yaml:"topics"`
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
		log.Printf("%s: %s\n", msg.Topic(), msg.Payload())
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
		if time.Now().Sub(lastMetricsTick).Minutes() > 5 {
			panic("been more than 5 mins and not receiving any more mqtt values. shutting down...")
		}
		lastMetricsTick = time.Now()
	}

	prometheus.MustRegister(promGauge)

	if token := mqttClient.Subscribe(gaugeConfig.MqttName, 0, handler); token.Wait() && token.Error() != nil {
		log.Println(token.Error())
		os.Exit(1)
	}
}

func contains(arr []string, str string) bool {
	for _, a := range arr {
		if a == str {
			return true
		}
	}
	return false
}

func exposeMqttSys(mqttClient mqtt.Client, config Config) {
	var handler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
		if contains(brokerMetricsIgnore, msg.Topic()) {
			return
		}

		f, _ := strconv.ParseFloat(string(msg.Payload()), 64)
		metricName := "mqtt_" + strings.ReplaceAll(strings.ReplaceAll(msg.Topic()[5:], "/", "_"), " ", "")

		if !strings.Contains(msg.Topic(), "broker") {
			log.Printf("%s: %s\n", msg.Topic(), msg.Payload())
			log.Printf("\t%s: %f\n", metricName, f)
		}

		if brokerGauge, ok := brokerMetrics[metricName]; ok {
			brokerGauge.WithLabelValues(config.Mqtt.URL, config.Mqtt.ClientID).Set(f)
		} else {
			brokerGauge := prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: metricName,
					Help: "Help: None " + metricName,
				},
				[]string{"url", "clientID"},
			)
			brokerGauge.WithLabelValues(config.Mqtt.URL, config.Mqtt.ClientID).Set(f)
			prometheus.MustRegister(brokerGauge)
			brokerMetrics[metricName] = brokerGauge
		}
	}

	if token := mqttClient.Subscribe("$SYS/#", 0, handler); token.Wait() && token.Error() != nil {
		log.Println("Unable to subscribe to $sys")
		log.Println(token.Error())
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

	mqtt.WARN = log.New(os.Stdout, "", 0)
	opts := mqtt.NewClientOptions().
		AddBroker(config.Mqtt.URL).
		SetClientID(config.Mqtt.ClientID).
		SetKeepAlive(5 * time.Second).
		SetPingTimeout(2 * time.Second).
		SetWriteTimeout(2 * time.Second).
		SetConnectTimeout(2 * time.Second).
		SetAutoReconnect(true).
		SetResumeSubs(true)

	mqttClient := mqtt.NewClient(opts)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	crony := cron.New()
	crony.AddFunc("* * * * *", func() {
		fmt.Println("publishig up time...")
		mqttClient.Publish(config.Mqtt.Topics.Uptime, 0, false, fmt.Sprintf("%d", time.Now().Unix()))
	})
	crony.Start()

	for _, gaugeConfig := range config.Prom.Gauges {
		buildGauge(gaugeConfig, mqttClient)
	}

	if config.Mqtt.ExposeSys {
		exposeMqttSys(mqttClient, config)
	}

	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(config.Prom.Http.Addr, nil))
}
