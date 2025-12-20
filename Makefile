# ----- Config -----
PROJECT            := uav-satellite-sim
NAMESPACE          := uav-sim
CLUSTER            := uav-sim

CONTROL_IMAGE      ?= uav-control:dev
DRONE_IMAGE        ?= uav-drone:dev

CONTROL_DOCKERFILE := deploy/docker/control.Dockerfile
DRONE_DOCKERFILE   := deploy/docker/drone.Dockerfile
K8S_CONTROL_YAML   := deploy/k8s/control.yaml
K8S_DRONE_YAML     := deploy/k8s/drone.yaml

PROTO              := proto/fleet.proto
GEN                := gen

CONTROL_SVC        := drone-control
GRPC_PORT          := 8081

# ----- Go ------
tidy:
	go mod tidy

test:
	go test ./...

proto:
	@rm -rf $(GEN)
	@mkdir -p $(GEN)
	protoc \
		--go_out=./$(GEN) \
		--go_opt=module=$(PROJECT) \
		--go-grpc_out=./$(GEN) \
		--go-grpc_opt=module=$(PROJECT) \
		$(PROTO)
	@echo "Generated into $(GEN)/"


# ----- Docker -----
docker-build:
	docker build -f $(CONTROL_DOCKERFILE) -t $(CONTROL_IMAGE) .
	docker build -f $(DRONE_DOCKERFILE)   -t $(DRONE_IMAGE)   .


# ----- k8s -----
kind-create:
	kind create cluster --name $(CLUSTER)

kind-delete:
	kind delete cluster --name $(CLUSTER)

kind-lock: docker-build
	kind load docker-image $(CONTROL_IMAGE) --name $(CLUSTER)
	kind load docker-image $(DRONE_IMAGE)   --name $(CLUSTER)

ns:
	@kubectl get namespace $(NAMESPACE) >/dev/null 2>&1 || kubectl create namespace $(NAMESPACE)

launch:
	kubectl apply -n $(NAMESPACE) -f $(K8S_CONTROL_YAML)
	kubectl apply -n $(NAMESPACE) -f $(K8S_DRONE_YAML)

unlaunch:
	kubectl delete -n $(NAMESPACE) -f $(K8S_CONTROL_YAML)
	kubectl delete -n $(NAMESPACE) -f $(K8S_DRONE_YAML)

