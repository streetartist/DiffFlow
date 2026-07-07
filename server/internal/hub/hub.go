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
	closed    bool
}

type SceneOwner struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	PeerID   string `json:"peer_id"`
	Path     string `json:"path"`
}

type Broker struct {
	mu          sync.RWMutex
	projects    map[int64]map[*Client]struct{}
	sceneOwners map[int64]map[string]SceneOwner
}

func NewBroker() *Broker {
	return &Broker{
		projects:    make(map[int64]map[*Client]struct{}),
		sceneOwners: make(map[int64]map[string]SceneOwner),
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
	b.releaseClientScenesLocked(c)
	b.closeClientLocked(c)
	log.Printf("[Project %d] client left: %s/%s", c.ProjectID, c.Username, c.PeerID)
}

func (b *Broker) ClaimScene(c *Client, path string) (SceneOwner, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sceneOwners[c.ProjectID] == nil {
		b.sceneOwners[c.ProjectID] = make(map[string]SceneOwner)
	}
	if owner, ok := b.sceneOwners[c.ProjectID][path]; ok && owner.PeerID != c.PeerID {
		return owner, false
	}
	owner := SceneOwner{
		UserID:   c.UserID,
		Username: c.Username,
		PeerID:   c.PeerID,
		Path:     path,
	}
	b.sceneOwners[c.ProjectID][path] = owner
	return owner, true
}

func (b *Broker) ReleaseScene(c *Client, path string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	owners := b.sceneOwners[c.ProjectID]
	if owners == nil {
		return false
	}
	owner, ok := owners[path]
	if !ok || owner.PeerID != c.PeerID {
		return false
	}
	delete(owners, path)
	if len(owners) == 0 {
		delete(b.sceneOwners, c.ProjectID)
	}
	return true
}

func (b *Broker) GetSceneOwner(projectID int64, path string) (SceneOwner, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	owners := b.sceneOwners[projectID]
	if owners == nil {
		return SceneOwner{}, false
	}
	owner, ok := owners[path]
	return owner, ok
}

func (b *Broker) releaseClientScenesLocked(c *Client) {
	owners := b.sceneOwners[c.ProjectID]
	if owners == nil {
		return
	}
	for path, owner := range owners {
		if owner.PeerID == c.PeerID {
			delete(owners, path)
		}
	}
	if len(owners) == 0 {
		delete(b.sceneOwners, c.ProjectID)
	}
}

func (b *Broker) closeClientLocked(c *Client) {
	if c.closed {
		return
	}
	close(c.Send)
	c.closed = true
}

func (b *Broker) Broadcast(projectID int64, event any) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("[Broker] marshal event: %v", err)
		return
	}
	b.BroadcastBytes(projectID, data)
}

func (b *Broker) SendToPeer(projectID int64, peerID string, event any) bool {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("[Broker] marshal event: %v", err)
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	for client := range b.projects[projectID] {
		if client.PeerID == peerID {
			select {
			case client.Send <- data:
				return true
			default:
				b.closeClientLocked(client)
				delete(b.projects[projectID], client)
				b.releaseClientScenesLocked(client)
				log.Printf("[Project %d] dropped slow client: %s/%s", projectID, client.Username, client.PeerID)
				return false
			}
		}
	}
	return false
}

func (b *Broker) BroadcastBytes(projectID int64, data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for client := range b.projects[projectID] {
		select {
		case client.Send <- data:
		default:
			b.closeClientLocked(client)
			delete(b.projects[projectID], client)
			b.releaseClientScenesLocked(client)
			log.Printf("[Project %d] dropped slow client: %s/%s", projectID, client.Username, client.PeerID)
		}
	}
}
