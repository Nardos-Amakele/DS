package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/dgrijalva/jwt-go"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Point struct {
	X, Y int
}

type Line struct {
	Start, End Point
	Color      string
}

type UndoMessage struct {
	Type string
}

type NotificationMessage struct {
	Type     string `json:"Type"`
	Username string `json:"Username"`
}

var (
	clients   = make(map[*websocket.Conn]string) // Connected clients with usernames
	broadcast = make(chan interface{})
	history   = []Line{}
	mutex     sync.Mutex
	jwtSecret = []byte("your-secret-key") // Use the same secret key here
)

func main() {
	r := mux.NewRouter()
	r.HandleFunc("/ws", handleConnections)

	go handleMessages()

	log.Println("Whiteboard Server started on :8082")
	log.Fatal(http.ListenAndServe(":8082", r))
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer ws.Close()

	username := r.URL.Query().Get("username")
	token := r.URL.Query().Get("token")

	// Validate JWT token
	if err := validateToken(token); err != nil {
		log.Println("Invalid token:", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	mutex.Lock()
	clients[ws] = username
	mutex.Unlock()

	broadcast <- NotificationMessage{Type: "user_joined", Username: username}

	go func() {
		mutex.Lock()
		historyCopy := append([]Line{}, history...)
		mutex.Unlock()
		for _, line := range historyCopy {
			if err := ws.WriteJSON(line); err != nil {
				log.Printf("Error sending history: %v\n", err)
				return
			}
		}
	}()

	for {
		var message map[string]interface{}
		if err := ws.ReadJSON(&message); err != nil {
			log.Printf("Read error: %v\n", err)
			mutex.Lock()
			delete(clients, ws)
			mutex.Unlock()
			broadcast <- NotificationMessage{Type: "user_left", Username: username}
			return
		}

		if message["Type"] == "undo" {
			mutex.Lock()
			if len(history) > 0 {
				history = history[:len(history)-1]
			}
			mutex.Unlock()

			broadcast <- UndoMessage{Type: "undo"}
		} else {
			var line Line
			jsonData, _ := json.Marshal(message)
			json.Unmarshal(jsonData, &line)

			mutex.Lock()
			history = append(history, line)
			mutex.Unlock()

			broadcast <- line
		}
	}
}

func validateToken(tokenString string) error {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return err
	}
	return nil
}

func handleMessages() {
	for msg := range broadcast {
		mutex.Lock()
		clientsCopy := make(map[*websocket.Conn]string, len(clients))
		for client, username := range clients {
			clientsCopy[client] = username
		}
		mutex.Unlock()

		for client := range clientsCopy {
			go func(client *websocket.Conn) {
				if err := client.WriteJSON(msg); err != nil {
					log.Printf("Broadcast error: %v\n", err)
					mutex.Lock()
					client.Close()
					delete(clients, client)
					mutex.Unlock()
				}
			}(client)
		}
	}
}
