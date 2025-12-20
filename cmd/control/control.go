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
	"google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
)


type droneSession struct {
	mailbox    chan *fleetv1.Command
	stopSender chan struct{}
	senderOnce sync.Once
}


type server struct {
	fleetv1.UnimplementedFleetControlServer

	mu	       sync.RWMutex
	drones     map[string]*fleetv1.DroneState
	lastSeen   map[string]time.Time

	sessions   map[string]*droneSession
}


func newServer() *server {
	return &server{
		drones:   make(map[string]*fleetv1.DroneState),
		lastSeen: make(map[string]time.Time),
		sessions: make(map[string]*droneSession),
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


func (s *server) getSessionLocked(id string) *droneSession {
	ses, ok := s.sessions[id]
	if !ok {
		ses = &droneSession{
			mailbox:    make(chan *fleetv1.Command, 64),
			stopSender: make(chan struct{}),
		}
		s.sessions[id] = ses
	}
	return ses
}


func (s *server) Register(ctx context.Context, req *fleetv1.RegisterRequest) (*fleetv1.RegisterResponse, error) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.drones[req.DroneId]; !ok {
		s.drones[req.DroneId] = &fleetv1.DroneState{
			DroneId: req.DroneId,
			Position: &fleetv1.Position{
				X: 0, Y: 0, Z: 0,
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


func (s *server) senderLoop(id string, stream fleetv1.FleetControl_SubscribeCommandsServer, mailbox <-chan *fleetv1.Command, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-stream.Context().Done():
			return
		case cmd := <-mailbox:
			if cmd == nil {
				continue
			}
			if err := stream.Send(cmd); err != nil {
				log.Printf("Send to drone=%s failed: %v", id, err)
				return
			}
		}
	}
}


func (s *server) SubscribeCommands(req *fleetv1.SubscribeCommandsRequest, stream fleetv1.FleetControl_SubscribeCommandsServer) error {
	id := req.GetDroneId()
	if id == "" {
		return status.Error(codes.InvalidArgument, "drone_id required!")
	}

	s.mu.Lock()
	ses := s.getSessionLocked(id)

	close(ses.stopSender)
	ses.stopSender = make(chan struct{})
	stop := ses.stopSender
	mailbox := ses.mailbox
	s.mu.Unlock()

	log.Printf("Commands stream opened for drone=%s", id)

	go s.senderLoop(id, stream, mailbox, stop)

	<-stream.Context().Done()

	s.mu.Lock()
	ses2 := s.sessions[id]
	if ses2 != nil {
		select {
		case <-ses2.stopSender:
		default:
			close(ses2.stopSender)
		}
	}
	s.mu.Unlock()

	log.Printf("Commands stream closed for drone=%s", id)
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
	id := req.GetDroneId()
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "drone_id required!")
	}

	mid := "m_" + randSeq(8)

	cmd := &fleetv1.Command{
		Payload: &fleetv1.Command_AssignMission{
			AssignMission: &fleetv1.Mission{
				MissionId: mid,
				Waypoints: req.GetWaypoints(),
			},
		},
	}

	s.mu.RLock()
	ses := s.sessions[id]
	s.mu.RUnlock()

	if ses == nil {
		return &fleetv1.AssignMissionResponse{MissionId: mid, Pushed: false}, nil
	}

	select {
	case ses.mailbox <- cmd:
		return &fleetv1.AssignMissionResponse{MissionId: mid, Pushed: true}, nil
	default:
		log.Printf("mailbox full for drone=%s, dropping command mission=%s", id, mid)
		return &fleetv1.AssignMissionResponse{MissionId: mid, Pushed: false}, nil
	}
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

