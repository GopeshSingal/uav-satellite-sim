package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net"
	"os"
	"sync"
	"time"

	fleetv1 "uav-satellite-sim/gen/fleet/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mission struct {
	id        string
	waypoints []*fleetv1.Position
	wp        int
}

type relayServer struct {
	fleetv1.UnimplementedDroneRelayServer

	selfID string
	peerSvc string

	onMission func(m * fleetv1.Mission)
}

func (r *relayServer) peerAddr(droneID string) string {
	return fmt.Sprintf("%s.%s:8082", droneID, r.peerSvc)
}

func (r *relayServer) RelayMission(ctx context.Context, msg *fleetv1.RelayMissionRequest) (*fleetv1.RelayAck, error) {
	if msg == nil || len(msg.Path) == 0 || msg.Mission == nil {
		return nil, status.Error(codes.InvalidArgument, "RelayMission requires mission and path!")
	}
	i := int(msg.Index)
	if i < 0 || i > len(msg.Path) {
		return nil, status.Error(codes.InvalidArgument, "RelayMission index out of range!")
	}
	if msg.Path[i] != r.selfID {
		return nil, status.Error(codes.InvalidArgument, "RelayMission path ID does not match self ID")
	}

	if i == len(msg.Path) - 1 {
		r.onMission(msg.Mission)
		return &fleetv1.RelayAck{Ok: true, Message: "delivered"}, nil
	}

	next := msg.Path[i + 1]
	conn, err := grpc.Dial(r.peerAddr(next), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return &fleetv1.RelayAck{Ok: false, Message: "Dial next hop failed"}, nil
	}
	defer conn.Close()

	_, err = fleetv1.NewDroneRelayClient(conn).RelayMission(ctx, &fleetv1.RelayMissionRequest{
		Mission: msg.Mission,
		Path:    msg.Path,
		Index:   uint32(i + 1),
	})
	if err != nil {
		return &fleetv1.RelayAck{Ok: false, Message: "Failed to forward"}, nil
	}
	return &fleetv1.RelayAck{Ok: true, Message: "Forwarded"}, nil
}

func (r *relayServer) RelayTelemetry(ctx context.Context, msg *fleetv1.RelayTelemetryRequest) (*fleetv1.RelayAck, error) {
	return &fleetv1.RelayAck{Ok: false, Message: "Not implemented"}, nil
}

func main() {
	rand.Seed(time.Now().UnixNano())

	control := mustEnv("CONTROL_ADDR")
	droneID := os.Getenv("DRONE_ID")
	if droneID == "" {
		droneID = "drone-" + randSeq(4)
	}

	peerSvc := os.Getenv("DRONE_HEADLESS_SERVICE")
	if peerSvc == "" {
		peerSvc = "drone-sim"
	}

	conn, err := grpc.Dial(control, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	client := fleetv1.NewFleetControlClient(conn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err = client.Register(ctx, &fleetv1.RegisterRequest{DroneId: droneID})
	if err != nil {
		log.Fatal(err)
	}

	cmdStream, err := client.SubscribeCommands(ctx, &fleetv1.SubscribeCommandsRequest{DroneId: droneID})
	if err != nil {
		log.Fatal(err)
	}
	telStream, err := client.TelemetryStream(ctx)
	if err != nil {
		log.Fatal(err)
	}

	state := &fleetv1.DroneState{
		DroneId: droneID,
		Position: &fleetv1.Position{
			X:   rand.Float64() * 50,
			Y:   rand.Float64() * 50,
			Z:	 rand.Float64() * 50,
		},
		Battery: 100,
		Status:  fleetv1.DroneStatus_DRONE_STATUS_IDLE,
	}

	var cur mission
	var curMu sync.RWMutex
	
	go func() {
		lis, err := net.Listen("tcp", ":8082")
		if err != nil {
			log.Fatalf("Relay listen failed: %v", err)
		}

		gs := grpc.NewServer()
		fleetv1.RegisterDroneRelayServer(gs, &relayServer{
			selfID: droneID,
			peerSvc: peerSvc,
			onMission: func(m *fleetv1.Mission) {
				curMu.Lock()
				cur = mission{id: m.MissionId, waypoints: m.Waypoints, wp: 0}
				curMu.Unlock()
				log.Printf("Relayed mission delivered=%s", m.MissionId)
			},
		})
		log.Printf("DroneRelay listening on :8082 (id=%s)", droneID)
		log.Fatal(gs.Serve(lis))
	}()

	go func() {
		for {
			cmd, err := cmdStream.Recv()
			if err != nil {
				log.Printf("Command stream closed: %v", err)	
				return
			}
			if m := cmd.GetAssignMission(); m != nil {
				curMu.Lock()
				cur = mission{id: m.MissionId, waypoints: m.Waypoints, wp: 0}
				curMu.Unlock()
				log.Printf("Received mission=%s", m.MissionId)
			}
			if rm := cmd.GetRelayMission(); rm != nil {
				i := int(rm.Index)
				if i < 0 || i >= len(rm.Path) || rm.Path[i] != droneID {
					log.Printf("Bad relay mission: idx=%d, path=%v, self=%s", i, rm.Path, droneID)
					continue
				}
				if i == len(rm.Path) - 1 {
					curMu.Lock()
					cur = mission{id: rm.Mission.MissionId, waypoints: rm.Mission.Waypoints, wp: 0}
					curMu.Unlock()
					log.Printf("Received relayed mission=%s", rm.Mission.MissionId)
					continue
				}
				next := rm.Path[i + 1]
				addr := fmt.Sprintf("%s.%s:8082", next, peerSvc)
				pc, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
				if err != nil {
					log.Printf("Dial next hop failed: %v", err)
					continue
				}
				_, err = fleetv1.NewDroneRelayClient(pc).RelayMission(ctx, &fleetv1.RelayMissionRequest{
					Mission: rm.Mission,
					Path: 	 rm.Path,
					Index:   uint32(i + 1),
				})
				_ = pc.Close()
				if err != nil {
					log.Printf("Forward relay mission failed: %v", err)
				}
			}
		}
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		curMu.RLock()
		active := cur.id != "" && cur.wp < len(cur.waypoints)
		var target *fleetv1.Position
		if active {
			target = cur.waypoints[cur.wp]
		}
		curMu.RUnlock()
		
		if active {
			state.Status = fleetv1.DroneStatus_DRONE_STATUS_EN_ROUTE
			if step(state.Position, target, 3) {
				curMu.Lock()
				cur.wp++
				curMu.Unlock()
			}
			state.Battery -= 0.3
			if state.Battery < 0 {
				state.Battery = 0
			}
		} else {
			state.Status = fleetv1.DroneStatus_DRONE_STATUS_IDLE
		}

		state.UpdatedAtUnixMs = time.Now().UnixMilli()
		if err := telStream.Send(&fleetv1.Telemetry{State: state}); err != nil {
			log.Printf("Telemetry send failed: %v", err)
		}
		log.Printf("Drone=%s, pos=(%.1f, %.1f, %.1f) batt=%.1f",
			droneID, state.Position.X, state.Position.Y, state.Position.Z, state.Battery)
	}
}

func step(p *fleetv1.Position, t *fleetv1.Position, s float64) bool {
	dx, dy, dz := t.X-p.X, t.Y-p.Y, t.Z-p.Z
	d := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if d < 0.01 {
		p.X, p.Y, p.Z = t.X, t.Y, t.Z
		return true
	}
	step := math.Min(s, d)
	p.X += dx / d * step
	p.Y += dy / d * step
	p.Z += dz / d * step
	return s >= d
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("Missing env %s", k)
	}
	return v
}

func randSeq(n int) string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
