
all: build-docker

build-docker:
	docker build -t mqtt-prometheus-exporter .
	
.PHONY:
	all build-docker