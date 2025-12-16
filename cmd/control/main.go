package main


import (
	"context"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	fleetv1 "uav-satellite-sim/gen/fleet/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)


type server struct {
	fleetv1.UnimplementedFleetControlServer

	mu	   sync.RWMutex
	drones     map[string]*fleetv1.DroneState
	cmdStreams map[string]fleetv1.FleetControl_SubscribeCommandsServer
	lastSeen   map[string]time.Time
}


func newServer() *server {
	return &server{
		drones: make(map[string]*fleetv1.DroneState),
		cmdStreams: make(map[string]fleetv1.FleetControl_SubscribeCommandsServer),
		lastSeen: make(map[string]time.Time),
	}
}


func main() {
	rand.Seed(time.Now().UnixNano())

	lis, err := net.Listen("tcp", ":8081")
	if err != nil {
		log.Fatal(err)
	}
	
	grpcServer := grpc.NewServer()
	fleetv1.RegisterFleetControlServer(grpcServer, newServer())

	reflection.Register(grpcServer)

	log.Println("Control gRPC listening on port 8081")
	log.Fatal(grpcServer.Serve(lis))
}


func (s *server) Register(ctx context.Context, req *fleetv1.RegisterRequest) (*fleetv1.RegisterResponse, error) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.drones[req.DroneId]; !ok {
		s.drones[req.DroneId] = &fleetv1.DroneState{
			DroneId: req.DroneId,
			Position: &fleetv1.Position{
				X: 0, Y: 0, Alt: 0,
			},
			Battery: 100,
			Status: fleetv1.DroneStatus_DRONE_STATUS_IDLE,
			UpdatedAtUnixMs: now.UnixMilli(),
		}
	}

	s.lastSeen[req.DroneId] = now
	log.Printf("Registered Drone=%s", req.DroneId)
	return &fleetv1.RegisterResponse{Ok: true}, nil
}


func (s *server) SubscribeCommands(req *fleetv1.SubscribeCommandsRequest, stream fleetv1.FleetControl_SubscribeCommandsServer) error {
	id := req.DroneId
	log.Printf("Commands stream open for drone=%s", id)

	s.mu.Lock()
	s.cmdStreams[id] = stream
	s.mu.Unlock()

	<-stream.Context().Done()

	s.mu.Lock()
	delete(s.cmdStreams, id)
	s.mu.Unlock()

	log.Printf("Commands stream closed for drone=%s)", id)
	return nil
}


func (s *server) TelemetryStream(stream fleetv1.FleetControl_TelemetryStreamServer) error {
	var count int64
	for {
		msg, err := stream.Recv()
		if err != nil {
			return stream.SendAndClose(&fleetv1.TelemetryAck{Received: count})
		}
		count++

		st := msg.State
		st.UpdatedAtUnixMs = time.Now().UnixMilli()

		s.mu.Lock()
		s.drones[st.DroneId] = st
		s.lastSeen[st.DroneId] = time.Now()
		s.mu.Unlock()
	}
}


func (s *server) AssignMission(ctx context.Context, req *fleetv1.AssignMissionRequest) (*fleetv1.AssignMissionResponse, error) {
	s.mu.RLock()
	stream, ok := s.cmdStreams[req.DroneId]
	s.mu.RUnlock()

	mid := "m_" + randSeq(8)
	if !ok {
		return &fleetv1.AssignMissionResponse{MissionId: mid, Pushed: false}, nil
	}

	cmd := &fleetv1.Command{
		Payload: &fleetv1.Command_AssignMission{
			AssignMission: &fleetv1.Mission{
				MissionId: mid,
				Waypoints: req.Waypoints,
			},
		},
	}

	if err := stream.Send(cmd); err != nil {
		return &fleetv1.AssignMissionResponse{MissionId: mid, Pushed: false}, nil
	}

	log.Printf("Mission pushed drone=%s mission=%s", req.DroneId, mid)
	return &fleetv1.AssignMissionResponse{MissionId: mid, Pushed: true}, nil
}


func (s *server) ListDrones(ctx context.Context, _ *fleetv1.ListDronesRequest) (*fleetv1.ListDronesResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*fleetv1.DroneState, 0, len(s.drones))
	for _, d := range s.drones {
		out = append(out, d)
	}
	return &fleetv1.ListDronesResponse{Drones: out}, nil
}


func randSeq(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

