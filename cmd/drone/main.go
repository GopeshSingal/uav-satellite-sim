package main

import (
	"context"
	"log"
	"math"
	"math/rand"
	"os"
	"time"

	fleetv1 "drone-fleet/gen/fleet/v1"

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
	ctx := context.Background()

	_, err = client.Register(ctx, &fleetv1.RegisterRequest{DroneId: droneID})
	if err != nil {
		log.Fatal(err)
	}

	cmdStream, _ := client.SubscribeCommands(ctx, &fleetv1.SubscribeCommandsRequest{DroneId: droneID})
	telStream, _ := client.TelemetryStream(ctx)

	state := &fleetv1.DroneState{
		DroneId: droneID,
		Position: &fleetv1.Position{
			X:   rand.Float64() * 50,
			Y:   rand.Float64() * 50,
			Alt: 10,
		},
		Battery: 100,
		Status:  fleetv1.DroneStatus_DRONE_STATUS_IDLE,
	}

	var cur mission

	go func() {
		for {
			cmd, err := cmdStream.Recv()
			if err != nil {
				return
			}
			if m := cmd.GetAssignMission(); m != nil {
				cur = mission{id: m.MissionId, waypoints: m.Waypoints}
				log.Printf("Received mission=%s", m.MissionId)
			}
		}
	}()

	ticker := time.NewTicker(1 * time.Second)
	for range ticker.C {
		if cur.id != "" && cur.wp < len(cur.waypoints) {
			state.Status = fleetv1.DroneStatus_DRONE_STATUS_EN_ROUTE
			if step(state.Position, cur.waypoints[cur.wp], 3) {
				cur.wp++
			}
			state.Battery -= 0.3
		} else {
			state.Status = fleetv1.DroneStatus_DRONE_STATUS_IDLE
		}

		state.UpdatedAtUnixMs = time.Now().UnixMilli()
		_ = telStream.Send(&fleetv1.Telemetry{State: state})

		log.Printf("Drone=%s pos=(%.1f, %.1f) batt=%.1f",
			droneID, state.Position.X, state.Position.Y, state.Battery)
	}
}

func step(p *fleetv1.Position, t *fleetv1.Position, s float64) bool {
	dx, dy := t.X-p.X, t.Y-p.Y
	d := math.Hypot(dx, dy)
	if d < 0.01 {
		p.X, p.Y, p.Alt = t.X, t.Y, t.Alt
		return true
	}

	p.X += dx / d * math.Min(s, d)
	p.Y += dy / d * math.Min(s, d)
	p.Alt = t.Alt
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
