package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

type Room struct {
	id        string
	players   map[string]*Player
	in_chan   chan INmessage
	terminate chan struct{}
	sync.Mutex
}

func (r *Room) Send(message OUTmessage) {
	for _, p := range r.players {
		p.Conn.WriteJSON(message)
	}
}


type Player struct {
	Id           string
	Conn         *websocket.Conn
	Name string
	X            float64
	Y            float64
	X_s          float64
	Y_s          float64
	// dodge time if time since last dodge, if time > kd - do dodge
	SpeedUpdated bool
}

type INmessage struct {
	Id     string
	Status string
	Name string `json:,omitempty`
	Text   string `json:,omitempty`
	X_s    float64    `json:,omitempty`
	Y_s    float64    `json:,omitempty`
}

type OUTmessage struct {
	Status string
	Name string  `json:,omitepty`
	Id     string `json:,omitepty`
	Text   string `json:,omitempty`
	X      float64    `json:,omitempty`
	Y      float64    `json:,omitempty`
	X_s    float64    `json:,omitempty`
	Y_s    float64   `json:,omitempty`
	Players []OUTmessage `json:,omitempty`
}

func (r *Room) Handle() {
	speed_ticker := time.NewTicker(Fps * time.Millisecond)
	check_ticker := time.NewTicker(300*time.Millisecond)
	prevTime := time.Now()
	for {
		select {
		case message := <-r.in_chan:
			fmt.Println("Message:", message)
			if _, ok := r.players[message.Id]; !ok{
				fmt.Println("invalid id, request skipped")
				continue
			}
			switch message.Status {
			case "move":
				r.players[message.Id].X_s, r.players[message.Id].Y_s = message.X_s, message.Y_s
				r.players[message.Id].SpeedUpdated = true
			case "connected":
				fmt.Println("User", message.Name, "connected")
				mainRoom.players[message.Id].Name = message.Name
				player_list := []OUTmessage{}
				for _, p := range r.players{
					player_list = append(player_list, OUTmessage{
						Id: p.Id,
						Name: p.Name,
						X_s: p.X_s,
						Y_s: p.Y_s,
						X: p.X,
						Y: p.Y,

					})
				}
				r.Send(OUTmessage{
					Status: "room_data",
					Players: player_list,
				})
				
			}
		case <-speed_ticker.C:
			time := time.Now()
			dt := float64( time.Sub(prevTime).Milliseconds() )
			prevTime = time
			for _, p := range r.players { 
				x_c := p.X+p.X_s*dt 
				y_c := p.Y+p.Y_s*dt
				p.X, p.Y = x_c, y_c
				if x_c < 0{
					p.X = -x_c
				} else if x_c + 20>FieldWidth{
					p.X = x_c - 20 - (x_c - FieldWidth)
				}
				if (y_c+20 > FieldHeight) {
					p.Y = y_c - 20 - (y_c - FieldHeight)
				} else if (y_c<0){
					p.Y = -y_c
				}
				//p.X, p.Y = p.X+p.X_s*dt, p.Y+p.Y_s*dt
				if p.SpeedUpdated{
					p.SpeedUpdated = false
					r.Send(OUTmessage{
						Status: "move",
						Id: p.Id,
						X_s: p.X_s,
						Y_s: p.Y_s,
					})
				}

			}
		case <-check_ticker.C:
			for _, p := range r.players{
				r.Send(OUTmessage{
					Status: "coords",
					Id: p.Id,
					X: p.X,
					Y: p.Y,
					Name: p.Name,	})
			}
		case <-r.terminate:
			r.players = map[string]*Player{}
			speed_ticker.Stop()
			check_ticker.Stop()
			fmt.Println("room terminated")
			return
		}
	}
}


func home(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("./static/index.html"))
	tmpl.Execute(w, nil)
}

func game(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("./static/game.html"))
	tmpl.Execute(w, nil)
}

func serve_ws(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		fmt.Println("ERROR WHILE CONNECTING:", err)
		return
	}
	id := uuid.New()
	mainRoom.Lock()
	mainRoom.players[id.String()] = &Player{Id: id.String(), Conn: conn, X: FieldWidth / 2, Y: FieldHeight / 2}
	mainRoom.Unlock()
	conn.WriteJSON(OUTmessage{Status: "id", Text: id.String(), X: 600 / 2, Y: 600 / 2})

	for _, pl := range mainRoom.players {
		for _, p := range mainRoom.players {
			pl.Conn.WriteJSON(OUTmessage{
				Status: "player_join",
				X:      p.X,
				Y:      p.Y,
				Id:     p.Id,
			})
		}
	}
	for {
		var message INmessage
		err := conn.ReadJSON(&message)
		if err != nil {
			fmt.Println("Connection lost", err, message, )
			mainRoom.Lock()
			for i, p := range mainRoom.players {
				if p.Conn == conn {
					mainRoom.Send(OUTmessage{
						Status: "playerLeft",
						Id: p.Id,
					})
					delete(mainRoom.players, i)
					break
				}
			}
			mainRoom.Unlock()
			break
		}
		for _, p := range mainRoom.players{
			if p.Conn == conn{
				message.Id = p.Id
				break
			}
		}
		mainRoom.in_chan <- message
	}
}

var r *mux.Router
var upgrader websocket.Upgrader
var mainRoom *Room

const (
	RectSize = 20
	FieldWidth = 900
	FieldHeight = 600
	Fps = 1000/60 //time.Millisecond
	
)

func init() {
	r = mux.NewRouter()
	mainRoom = &Room{}
	mainRoom.id = uuid.New().String()
	mainRoom.players = make(map[string]*Player)
	mainRoom.in_chan = make(chan INmessage)
	mainRoom.terminate = make(chan struct{})
	go mainRoom.Handle()
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

}

func main() {
	r.HandleFunc("/", home)
	r.HandleFunc("/game", game)
	r.HandleFunc("/ws", serve_ws).Methods("GET")
	log.Fatal(http.ListenAndServe(":8000", r))
}
