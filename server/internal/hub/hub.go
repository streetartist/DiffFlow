package hub

import (
	"encoding/json"
	"log"
	"sync"
)

type Client struct {
	UserID    int64
	Username  string
	PeerID    string
	ProjectID int64
	Send      chan []byte
}

type Broker struct {
	mu       sync.RWMutex
	projects map[int64]map[*Client]struct{}
}

func NewBroker() *Broker {
	return &Broker{
		projects: make(map[int64]map[*Client]struct{}),
	}
}

func (b *Broker) Register(c *Client) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.projects[c.ProjectID] == nil {
		b.projects[c.ProjectID] = make(map[*Client]struct{})
	}
	b.projects[c.ProjectID][c] = struct{}{}
	log.Printf("[Project %d] client joined: %s/%s", c.ProjectID, c.Username, c.PeerID)
}

func (b *Broker) Unregister(c *Client) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if clients := b.projects[c.ProjectID]; clients != nil {
		delete(clients, c)
		if len(clients) == 0 {
			delete(b.projects, c.ProjectID)
		}
	}
	close(c.Send)
	log.Printf("[Project %d] client left: %s/%s", c.ProjectID, c.Username, c.PeerID)
}

func (b *Broker) Broadcast(projectID int64, event any) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("[Broker] marshal event: %v", err)
		return
	}
	b.BroadcastBytes(projectID, data)
}

func (b *Broker) BroadcastBytes(projectID int64, data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for client := range b.projects[projectID] {
		select {
		case client.Send <- data:
		default:
			close(client.Send)
			delete(b.projects[projectID], client)
			log.Printf("[Project %d] dropped slow client: %s/%s", projectID, client.Username, client.PeerID)
		}
	}
}
