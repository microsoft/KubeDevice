BUILD_DIR ?= _output

.PHONY: all
all: clean kube-scheduler kubecri

.PHONY: kube-scheduler
kube-scheduler:
	go build -o ${BUILD_DIR}/kube-scheduler ./kube-scheduler/cmd/scheduler.go

.PHONY: kubecri
kubecri:
	go build -o ${BUILD_DIR}/kubecri ./kubecri/cmd/kubecri.go

.PHONY: clean
clean:
	rm -rf ${BUILD_DIR}/*

.PHONY: test
test:
	cd ./device-scheduler/device; go test; cd ../../kubeinterface; go test
