package main

import (
	"context"
	"log"
	"math"
	"math/rand"
	"os"
	"sync"
	"time"

	fleetv1 "uav-satellite-sim/gen/fleet/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type mission struct {
	id        string
	waypoints []*fleetv1.Position
	wp        int
}

func main() {
	rand.Seed(time.Now().UnixNano())

	control := mustEnv("CONTROL_ADDR")
	droneID := os.Getenv("DRONE_ID")
	if droneID == "" {
		droneID = "drone-" + randSeq(4)
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
