package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
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
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
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
		if event["type"] == "presence" {
			event["project_id"] = client.ProjectID
			event["user_id"] = client.UserID
			event["username"] = client.Username
			event["peer_id"] = client.PeerID
			broker.Broadcast(client.ProjectID, event)
		}
	}
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
