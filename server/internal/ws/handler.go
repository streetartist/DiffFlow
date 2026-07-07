package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/diffflow/server/internal/auth"
	"github.com/diffflow/server/internal/hub"
	"github.com/diffflow/server/internal/store"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 64 * 1024
)

func HandleWebSocket(broker *hub.Broker, db *store.DB, tokens *auth.TokenManager, w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	claims, err := tokens.Parse(token)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	user, err := db.GetUserByID(claims.UserID)
	if err != nil || !user.Enabled {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	projectID, err := strconv.ParseInt(r.URL.Query().Get("project_id"), 10, 64)
	if err != nil || projectID <= 0 {
		http.Error(w, "project_id is required", http.StatusBadRequest)
		return
	}
	ok, err := db.CanAccessProject(user.ID, projectID, user.IsAdmin)
	if err != nil {
		http.Error(w, "project lookup failed", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS] Upgrade error: %v", err)
		return
	}
	conn.SetReadLimit(maxMessageSize)

	peerID := r.URL.Query().Get("peer_id")
	if peerID == "" {
		peerID = user.Username
	}

	client := &hub.Client{
		UserID:    user.ID,
		Username:  user.Username,
		PeerID:    peerID,
		ProjectID: projectID,
		Send:      make(chan []byte, 64),
	}

	broker.Register(client)

	go writePump(conn, client)
	go readPump(conn, client, broker)
}

func readPump(conn *websocket.Conn, client *hub.Client, broker *hub.Broker) {
	defer func() {
		broker.Unregister(client)
		conn.Close()
	}()

	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[WS] Read error: %v", err)
			}
			break
		}

		var event map[string]any
		if err := json.Unmarshal(message, &event); err != nil {
			continue
		}
		switch event["type"] {
		case "presence":
			event["project_id"] = client.ProjectID
			event["user_id"] = client.UserID
			event["username"] = client.Username
			event["peer_id"] = client.PeerID
			broker.Broadcast(client.ProjectID, event)
		case "scene_opened":
			path, ok := normalizeScenePath(event["path"])
			if !ok {
				continue
			}
			owner, claimed := broker.ClaimScene(client, path)
			if !claimed {
				broker.SendToPeer(client.ProjectID, client.PeerID, map[string]any{
					"type":              "scene_busy",
					"path":              path,
					"project_id":        client.ProjectID,
					"target_peer_id":    client.PeerID,
					"requester_peer_id": client.PeerID,
					"owner_peer_id":     owner.PeerID,
					"owner_username":    owner.Username,
					"owner_user_id":     owner.UserID,
					"peer_id":           owner.PeerID,
					"username":          owner.Username,
					"user_id":           owner.UserID,
				})
				continue
			}
			broker.Broadcast(client.ProjectID, sceneEvent("scene_opened", path, client))
		case "scene_released":
			path, ok := normalizeScenePath(event["path"])
			if !ok {
				continue
			}
			if broker.ReleaseScene(client, path) {
				broker.Broadcast(client.ProjectID, sceneEvent("scene_released", path, client))
			}
		case "scene_takeover_request":
			path, ok := normalizeScenePath(event["path"])
			if !ok {
				continue
			}
			targetPeerID, ok := normalizePeerID(event["target_peer_id"])
			if !ok {
				continue
			}
			owner, locked := broker.GetSceneOwner(client.ProjectID, path)
			if !locked || owner.PeerID == client.PeerID {
				broker.SendToPeer(client.ProjectID, client.PeerID, map[string]any{
					"type":           "scene_takeover_approved",
					"path":           path,
					"project_id":     client.ProjectID,
					"target_peer_id": client.PeerID,
					"peer_id":        targetPeerID,
					"username":       owner.Username,
					"user_id":        owner.UserID,
				})
				continue
			}
			if owner.PeerID != targetPeerID {
				broker.SendToPeer(client.ProjectID, client.PeerID, map[string]any{
					"type":              "scene_busy",
					"path":              path,
					"project_id":        client.ProjectID,
					"target_peer_id":    client.PeerID,
					"requester_peer_id": client.PeerID,
					"owner_peer_id":     owner.PeerID,
					"owner_username":    owner.Username,
					"owner_user_id":     owner.UserID,
					"peer_id":           owner.PeerID,
					"username":          owner.Username,
					"user_id":           owner.UserID,
				})
				continue
			}
			broker.SendToPeer(client.ProjectID, targetPeerID, map[string]any{
				"type":               "scene_takeover_request",
				"path":               path,
				"project_id":         client.ProjectID,
				"target_peer_id":     targetPeerID,
				"requester_peer_id":  client.PeerID,
				"requester_username": client.Username,
				"requester_user_id":  client.UserID,
				"peer_id":            client.PeerID,
				"username":           client.Username,
				"user_id":            client.UserID,
			})
		case "scene_takeover_approved":
			path, ok := normalizeScenePath(event["path"])
			if !ok {
				continue
			}
			targetPeerID, ok := normalizePeerID(event["target_peer_id"])
			if !ok {
				continue
			}
			if owner, locked := broker.GetSceneOwner(client.ProjectID, path); locked && owner.PeerID != client.PeerID {
				continue
			}
			broker.ReleaseScene(client, path)
			out := sceneEvent("scene_takeover_approved", path, client)
			out["target_peer_id"] = targetPeerID
			if sha, ok := normalizeSHA(event["sha256"]); ok {
				out["sha256"] = sha
			}
			broker.SendToPeer(client.ProjectID, targetPeerID, out)
		case "scene_takeover_denied":
			path, ok := normalizeScenePath(event["path"])
			if !ok {
				continue
			}
			targetPeerID, ok := normalizePeerID(event["target_peer_id"])
			if !ok {
				continue
			}
			out := sceneEvent("scene_takeover_denied", path, client)
			out["target_peer_id"] = targetPeerID
			if reason := normalizeReason(event["reason"]); reason != "" {
				out["reason"] = reason
			}
			broker.SendToPeer(client.ProjectID, targetPeerID, out)
		}
	}
}

func sceneEvent(eventType string, path string, client *hub.Client) map[string]any {
	return map[string]any{
		"type":       eventType,
		"path":       path,
		"project_id": client.ProjectID,
		"user_id":    client.UserID,
		"username":   client.Username,
		"peer_id":    client.PeerID,
	}
}

func normalizeScenePath(value any) (string, bool) {
	path, ok := value.(string)
	if !ok {
		return "", false
	}
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	path = strings.TrimPrefix(path, "res://")
	for strings.HasPrefix(path, "/") {
		path = strings.TrimPrefix(path, "/")
	}
	if path == "" || strings.Contains(path, ":") || strings.Contains(path, "//") {
		return "", false
	}
	if !(strings.HasSuffix(path, ".tscn") || strings.HasSuffix(path, ".scn")) {
		return "", false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	return path, true
}

func normalizePeerID(value any) (string, bool) {
	peerID, ok := value.(string)
	if !ok {
		return "", false
	}
	peerID = strings.TrimSpace(peerID)
	if peerID == "" || len(peerID) > 128 {
		return "", false
	}
	for _, ch := range peerID {
		if ch < 33 || ch == 127 {
			return "", false
		}
	}
	return peerID, true
}

func normalizeSHA(value any) (string, bool) {
	sha, ok := value.(string)
	if !ok {
		return "", false
	}
	sha = strings.TrimSpace(strings.ToLower(sha))
	if sha == "" {
		return "", false
	}
	if len(sha) != 64 {
		return "", false
	}
	for _, ch := range sha {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return "", false
		}
	}
	return sha, true
}

func normalizeReason(value any) string {
	reason, ok := value.(string)
	if !ok {
		return ""
	}
	reason = strings.TrimSpace(reason)
	runes := []rune(reason)
	if len(runes) > 240 {
		reason = string(runes[:240])
	}
	return reason
}

func writePump(conn *websocket.Conn, client *hub.Client) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.Send:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
