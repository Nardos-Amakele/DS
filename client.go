package main

import (
	"bytes"
	"encoding/json"
	"image/color"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
)

var (
	serverAddr     = "localhost:8082"        // Whiteboard server address
	authServerAddr = "http://localhost:8081" // Auth server address
)

type User struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type Point struct {
	X, Y int
}

type Line struct {
	Start, End Point
	Color      string
}

type Whiteboard struct {
	Lines []Line
	mutex sync.Mutex
	conn  *websocket.Conn // Include the connection
}

var prevPoint *Point
var drawColor = "black"

// Global variable to track if a game is already running
var gameRunning bool

func main() {
	http.HandleFunc("/login", loginPage)
	http.HandleFunc("/register", registerPage)
	log.Println("Client server started on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func loginPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if gameRunning {
			log.Println("Game is already running. Please close the current instance before logging in again.")
			http.Error(w, "Game is already running", http.StatusConflict)
			return
		}

		// Retrieve username and password
		username := r.FormValue("username")
		password := r.FormValue("password")

		user := User{Username: username, Password: password}
		loginPayload, err := json.Marshal(user)
		if err != nil {
			log.Println("Error marshalling JSON:", err)
			http.Error(w, "Failed to create request", http.StatusInternalServerError)
			return
		}

		resp, err := http.Post(authServerAddr+"/login", "application/json", bytes.NewBuffer(loginPayload))
		if err != nil {
			log.Println("Error during login request:", err)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Println("Login failed with status:", resp.Status)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Connect to the whiteboard server
		u := "ws://" + serverAddr + "/ws?username=" + username
		conn, _, err := websocket.DefaultDialer.Dial(u, nil)
		if err != nil {
			log.Fatal("Connection error:", err)
		}

		// Create a new instance of the whiteboard for this user
		whiteboard := &Whiteboard{conn: conn}

		// Mark the game as running
		gameRunning = true

		// Start the Ebiten game
		ebiten.SetWindowSize(800, 600)
		ebiten.SetWindowTitle("Distributed Whiteboard - " + username)
		go whiteboard.listenForMessages() // Listen for incoming messages
		if err := ebiten.RunGame(whiteboard); err != nil {
			log.Fatal(err)
		}

		// Once the game loop ends, mark it as not running
		gameRunning = false
		return
	}

	// Render the login form (HTML template can be used here)
	http.ServeFile(w, r, "templates/login.html")
}

func registerPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// Retrieve username and password from the form
		username := r.FormValue("username")
		password := r.FormValue("password")

		user := User{Username: username, Password: password}
		registerPayload, err := json.Marshal(user)
		if err != nil {
			log.Println("Error marshalling JSON:", err)
			http.Error(w, "Failed to create request", http.StatusInternalServerError)
			return
		}

		resp, err := http.Post(authServerAddr+"/register", "application/json", bytes.NewBuffer(registerPayload))
		if err != nil {
			log.Println("Error during registration request:", err)
			http.Error(w, "Registration failed", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			log.Println("Registration failed with status:", resp.Status)
			http.Error(w, "Registration failed", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Render the registration form
	http.ServeFile(w, r, "templates/register.html")
}

func (wb *Whiteboard) listenForMessages() {
	for {
		_, msg, err := wb.conn.ReadMessage()
		if err != nil {
			log.Println("Read error:", err)
			return
		}

		var line Line
		if err := json.Unmarshal(msg, &line); err == nil {
			wb.mutex.Lock()
			wb.Lines = append(wb.Lines, line)
			wb.mutex.Unlock()
		} else {
			log.Println("Error unmarshalling message:", err)
		}
	}
}

func (wb *Whiteboard) Update() error {
	if wb.conn == nil {
		log.Println("WebSocket connection is nil")
		return nil // Handle the error appropriately
	}

	if ebiten.IsKeyPressed(ebiten.Key1) {
		drawColor = "black"
	} else if ebiten.IsKeyPressed(ebiten.Key2) {
		drawColor = "red"
	} else if ebiten.IsKeyPressed(ebiten.Key3) {
		drawColor = "blue"
	} else if ebiten.IsKeyPressed(ebiten.Key4) {
		drawColor = "green"
	}

	if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		x, y := ebiten.CursorPosition()
		currentPoint := Point{X: x, Y: y}

		if prevPoint != nil {
			line := Line{Start: *prevPoint, End: currentPoint, Color: drawColor}

			wb.mutex.Lock()
			wb.Lines = append(wb.Lines, line)
			wb.mutex.Unlock()

			data, _ := json.Marshal(line)
			if err := wb.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Println("Write error:", err)
			}
		}
		prevPoint = &currentPoint
	} else {
		prevPoint = nil
	}

	if ebiten.IsKeyPressed(ebiten.KeyU) {
		wb.mutex.Lock()
		if len(wb.Lines) > 0 {
			wb.Lines = wb.Lines[:len(wb.Lines)-1]
		}
		wb.mutex.Unlock()

		undoMsg := map[string]string{"Type": "undo"}
		data, _ := json.Marshal(undoMsg)
		if err := wb.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Println("Undo send error:", err)
		}
	}
	return nil
}

func (wb *Whiteboard) Draw(screen *ebiten.Image) {
	screen.Fill(color.White)

	wb.mutex.Lock()
	for _, line := range wb.Lines {
		ebitenutil.DrawLine(screen, float64(line.Start.X), float64(line.Start.Y), float64(line.End.X), float64(line.End.Y), getColor(line.Color))
	}
	wb.mutex.Unlock()
}

func getColor(colorName string) color.Color {
	switch colorName {
	case "red":
		return color.RGBA{255, 0, 0, 255}
	case "blue":
		return color.RGBA{0, 0, 255, 255}
	case "green":
		return color.RGBA{0, 255, 0, 255}
	default:
		return color.Black
	}
}

func (wb *Whiteboard) Layout(outsideWidth, outsideHeight int) (screenWidth, screenHeight int) {
	return 800, 600
}
