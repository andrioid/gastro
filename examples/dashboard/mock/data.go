package mock

import (
	"fmt"
	"math/rand"
	"sync"
)

type AgentStatus string

const (
	StatusAvailable AgentStatus = "available"
	StatusOnCall    AgentStatus = "oncall"
	StatusAway      AgentStatus = "away"
)

type Agent struct {
	Name    string
	Status  AgentStatus
	CallDur string
}

type Dashboard struct {
	ActiveCalls int
	AvgWaitSecs int
	CallsToday  int
	QueueDepth  int
	Agents      []Agent
}

var agentNames = []string{
	"Sofie Andersen",
	"Mikkel Hansen",
	"Ida Christensen",
	"Lars Pedersen",
	"Freja Nielsen",
	"Jonas Larsen",
	"Astrid Rasmussen",
	"Emil Jorgensen",
	"Nanna Madsen",
	"Oscar Thomsen",
}

// agentState tracks each agent's evolving state between ticks.
type agentState struct {
	status       AgentStatus
	callSeconds  int // how long the current call has lasted
	stateSeconds int // how long in current status (for transitions)
}

// simulator holds persistent state across Generate() calls.
type simulator struct {
	mu          sync.Mutex
	agents      []agentState
	callsToday  int
	queueDepth  int
	avgWaitSecs int
	initialized bool
}

var sim simulator

func (s *simulator) init() {
	s.agents = make([]agentState, len(agentNames))
	for i := range s.agents {
		r := rand.Float64()
		switch {
		case r < 0.5:
			s.agents[i] = agentState{status: StatusOnCall, callSeconds: rand.Intn(300)}
		case r < 0.85:
			s.agents[i] = agentState{status: StatusAvailable}
		default:
			s.agents[i] = agentState{status: StatusAway, stateSeconds: rand.Intn(120)}
		}
	}
	s.callsToday = 140 + rand.Intn(60)
	s.queueDepth = 1 + rand.Intn(4)
	s.avgWaitSecs = 15 + rand.Intn(20)
	s.initialized = true
}

// tick advances the simulation by ~3 seconds (one SSE interval).
func (s *simulator) tick() {
	for i := range s.agents {
		a := &s.agents[i]
		a.stateSeconds += 3

		switch a.status {
		case StatusOnCall:
			a.callSeconds += 3

			// ~8% chance per tick to finish the call
			if rand.Float64() < 0.08 {
				a.status = StatusAvailable
				a.callSeconds = 0
				a.stateSeconds = 0
				s.callsToday++
			}

		case StatusAvailable:
			// ~12% chance per tick to take a new call
			if rand.Float64() < 0.12 {
				a.status = StatusOnCall
				a.callSeconds = 0
				a.stateSeconds = 0
			}
			// ~2% chance to go on break
			if rand.Float64() < 0.02 {
				a.status = StatusAway
				a.stateSeconds = 0
			}

		case StatusAway:
			// Come back after roughly 30-90 seconds
			if a.stateSeconds > 30 && rand.Float64() < 0.15 {
				a.status = StatusAvailable
				a.stateSeconds = 0
			}
		}
	}

	// Queue drifts gently: -1, 0, or +1
	drift := rand.Intn(3) - 1
	s.queueDepth += drift
	if s.queueDepth < 0 {
		s.queueDepth = 0
	}
	if s.queueDepth > 15 {
		s.queueDepth = 15
	}

	// Avg wait drifts by -2..+2
	s.avgWaitSecs += rand.Intn(5) - 2
	if s.avgWaitSecs < 5 {
		s.avgWaitSecs = 5
	}
	if s.avgWaitSecs > 90 {
		s.avgWaitSecs = 90
	}
}

// Generate returns the current dashboard state and advances the simulation.
func Generate() Dashboard {
	sim.mu.Lock()
	defer sim.mu.Unlock()

	if !sim.initialized {
		sim.init()
	} else {
		sim.tick()
	}

	agents := make([]Agent, len(agentNames))
	activeCalls := 0

	for i, name := range agentNames {
		a := sim.agents[i]
		var dur string

		if a.status == StatusOnCall {
			activeCalls++
			mins := a.callSeconds / 60
			secs := a.callSeconds % 60
			dur = fmt.Sprintf("%d:%02d", mins, secs)
		}

		agents[i] = Agent{
			Name:    name,
			Status:  a.status,
			CallDur: dur,
		}
	}

	return Dashboard{
		ActiveCalls: activeCalls,
		AvgWaitSecs: sim.avgWaitSecs,
		CallsToday:  sim.callsToday,
		QueueDepth:  sim.queueDepth,
		Agents:      agents,
	}
}
