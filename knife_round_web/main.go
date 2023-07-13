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

type Server struct {
	List map[string]*Room
	sync.Mutex
}

func (p *Server) Get(id string) *Room {
	p.Lock()
	pl, ok := p.List[id]
	p.Unlock()
	if !ok {
		return nil
	}
	return pl

}

func (p *Server) Delete(id string) {
	p.Lock()
	delete(p.List, id)
	p.Unlock()

}

func (p *Server) Add(r *Room) {
	p.Lock()
	p.List[r.id] = r
	p.Unlock()

}

type ToChan struct {
	message INmessage
	player  *Player
}

type Room struct {
	id         string
	players    map[string]*Player
	in_chan    chan ToChan
	alive_chan chan *Player
	terminate  chan struct{}
	sync.Mutex
}

func RoomInit(id string) *Room {
	room := &Room{
		id:        id,
		in_chan:   make(chan ToChan),
		terminate: make(chan struct{}),
		alive_chan: make(chan *Player),
		players:   make(map[string]*Player),
	}
	return room
}

func (r *Room) Send(message OUTmessage) {
	for _, p := range r.players {
		p.Conn.WriteJSON(message)
	}
}

var Users Players

type Players struct {
	List map[string]*Player
	sync.Mutex
}

func (p *Players) Get(id string) *Player {
	p.Lock()
	pl, ok := p.List[id]
	p.Unlock()
	if !ok {
		return nil
	}
	return pl

}

func (p *Players) Add(pl *Player) {
	p.Lock()
	p.List[pl.Id] = pl
	p.Unlock()
}

func (p *Players) Delete(id string) {
	p.Lock()
	delete(p.List, id)
	p.Unlock()
}

func (p *Players) Full(id string) map[string]*Player {
	var l map[string]*Player
	p.Lock()
	l = p.List
	p.Unlock()
	return l
}

type Player struct {
	Id           string
	Room         *Room
	Conn         *websocket.Conn
	Name         string
	X            float64
	Y            float64
	X_s          float64
	Y_s          float64
	Stats        GameStats
	SpeedUpdated bool
	sync.Mutex
}

func (player *Player) CheckHit() []*Player {
	var hitted []*Player
	if time.Now().Sub(player.Stats.LastHit).Seconds() >= HitTime.Seconds() {
		player.Room.Send(OUTmessage{
			Status: "move",
			Text:   "hit",
			Id:     player.Id,
		})
		player.Stats.LastHit = time.Now()
		HitSquareX := player.X - 20
		HitSquareY := player.Y - 20
		fmt.Println("Hit coords: ", HitSquareX, HitSquareY)
		for _, user := range player.Room.players {
			if user.Id == player.Id {
				continue
			}
			//&&((HitSquareY >= user.Y && HitSquareY <= user.Y+PlayerHeigth) || (HitSquareY+HitHeigth >= user.Y && HitSquareY+HitHeigth <= user.Y+PlayerHeigth))
			if ((user.X >= HitSquareX && user.X <= HitSquareX+HitWidth) || (user.X+PlayerWidth >= HitSquareX && user.X+PlayerWidth <= HitSquareX+HitWidth)) && ((user.Y >= HitSquareY && user.Y <= HitSquareY+HitHeigth) || (user.Y+PlayerHeigth >= HitSquareY && user.Y+PlayerHeigth <= HitSquareY+HitHeigth)) {
				hitted = append(hitted, user)
			}
		}
	}
	return hitted
}

type GameStats struct {
	Hp      int
	LastHit time.Time
	Alive   bool
}

type INmessage struct {
	Id     string `json:,omitempty`
	Status string
	Room   string  `json:,omitempty`
	Name   string  `json:,omitempty`
	Text   string  `json:,omitempty`
	X_s    float64 `json:,omitempty`
	Y_s    float64 `json:,omitempty`
}

type OUTmessage struct {
	Status  string
	Name    string       `json:,omitepty`
	Id      string       `json:,omitepty`
	Text    string       `json:,omitempty`
	X       float64      `json:,omitempty`
	Y       float64      `json:,omitempty`
	X_s     float64      `json:,omitempty`
	Y_s     float64      `json:,omitempty`
	Players []OUTmessage `json:,omitempty`
}

func (r *Room) Handle() {
	speed_ticker := time.NewTicker(Fps * time.Millisecond)
	check_ticker := time.NewTicker(300 * time.Millisecond)
	prevTime := time.Now()
	for {
		select {
		case data := <-r.in_chan:
			message := data.message
			player := data.player
			if player.Room.id != r.id {
				fmt.Println("invalid id, request skipped")
				continue
			}
			switch message.Status {
			case "move":
				player.X_s, player.Y_s = message.X_s, message.Y_s
				player.SpeedUpdated = true
				if message.Text == "hit" {
					deadlist := player.CheckHit()
					for _, user_dead := range deadlist {
						fmt.Println("DEAD:", user_dead.Name)
						user_dead.Stats.Alive = false
						r.Send(OUTmessage{
							Status: "killed",
							Text:   user_dead.Id,
							Id:     player.Id,
						})
						go func(deadp *Player){
							<-time.After(3*time.Second)
							r.alive_chan <- deadp
						}(user_dead)
					}
				}
			case "connected":
				fmt.Println("User:", message.Name)
				player_list := []OUTmessage{}
				for _, p := range r.players {
					if !p.Stats.Alive {
						continue
					}
					player_list = append(player_list, OUTmessage{
						Id:   p.Id,
						Name: p.Name,
						X_s:  p.X_s,
						Y_s:  p.Y_s,
						X:    p.X,
						Y:    p.Y,
					})
				}
				r.Send(OUTmessage{
					Status:  "room_data",
					Players: player_list,
				})
			}
		case to_alive := <-r.alive_chan:
			to_alive.Stats.Alive = true
			to_alive.X = FieldWidth/2
			to_alive.Y = FieldHeight/2
			r.Send(OUTmessage{
				Status: "alive",
				Id:     to_alive.Id,
				X: to_alive.X,
				Y: to_alive.Y,
			})
		case <-speed_ticker.C:
			time := time.Now()
			dt := float64(time.Sub(prevTime).Milliseconds())
			prevTime = time
			for _, p := range r.players {
				if !p.Stats.Alive {
					continue
				}
				x_c := p.X + p.X_s*dt
				y_c := p.Y + p.Y_s*dt
				p.X, p.Y = x_c, y_c
				if x_c < 0 {
					p.X = x_c - x_c
				} else if x_c+20 > FieldWidth {
					p.X = x_c - 20 - (x_c - FieldWidth)
				}
				if y_c+20 > FieldHeight {
					p.Y = y_c - 20 - (y_c - FieldHeight)
				} else if y_c < 0 {
					p.Y = y_c - y_c
				}
				//p.X, p.Y = p.X+p.X_s*dt, p.Y+p.Y_s*dt
				if p.SpeedUpdated {
					m := OUTmessage{
						Status: "move",
						Id:     p.Id,
						X_s:    p.X_s,
						Y_s:    p.Y_s,
					}
					p.SpeedUpdated = false
					r.Send(m)
				}
			}
		case <-check_ticker.C:
			for _, p := range r.players {
				r.Send(OUTmessage{
					Status: "coords",
					Id:     p.Id,
					X:      p.X,
					Y:      p.Y})
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

func home(w http.ResponseWriter, _ *http.Request) {
	tmpl := template.Must(template.ParseFiles("./static/index.html"))
	tmpl.Execute(w, nil)
}

func game(w http.ResponseWriter, _ *http.Request) {
	tmpl := template.Must(template.ParseFiles("./static/game.html"))
	tmpl.Execute(w, nil)
}

func RoomMidleware(m INmessage, pl *Player) bool {
	switch m.Status {
	case "move":
		if pl.Stats.Alive && m.X_s <= 0.2 && m.X_s >= -0.2 && m.Y_s <= 0.2 && m.Y_s >= -0.2 {
			return true
		}
		break
	default:
		return true
	}
	return false
}

func serve_ws(w http.ResponseWriter, r *http.Request) {
	var player_room *Room
	var pl *Player
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		fmt.Println("ERROR WHILE CONNECTING:", err)
		return
	}
	pl = &Player{Id: uuid.New().String(), Room: nil, Conn: conn, X: FieldWidth / 2, Y: FieldHeight / 2, Stats: GameStats{Hp: 3, LastHit: time.Now(), Alive: true}}
	Users.Add(pl)
	conn.WriteJSON(OUTmessage{Status: "id", Text: pl.Id, X: 600 / 2, Y: 600 / 2})

	for {
		var message INmessage
		err := conn.ReadJSON(&message)
		if err != nil {
			fmt.Println("Connection lost", err, message)
			player_room.Lock()
			delete(player_room.players, pl.Id)
			player_room.Unlock()
			player_room.Send(OUTmessage{
				Status: "playerLeft",
				Id:     pl.Id,
			})
			Users.Delete(pl.Id)
			break
		}
		if message.Status == "connected" {
			player_room = Rooms.Get(message.Room)
			if player_room == nil {
				conn.Close()
				return
			}
			pl.Name = message.Name
			pl.Room = player_room
			player_room.Lock()
			player_room.players[pl.Id] = pl
			player_room.Unlock()
		}
		if RoomMidleware(message, pl) {
			player_room.in_chan <- ToChan{message, pl}
		}
		//}
	}
}

var r *mux.Router
var upgrader websocket.Upgrader
var Rooms Server

const (
	RectSize     = 20
	FieldWidth   = 900
	FieldHeight  = 600
	Fps          = 1000 / 60 //time.Millisecond
	HitTime      = 1500 * time.Millisecond
	PlayerWidth  = 20
	PlayerHeigth = 20
	HitHeigth    = 60
	HitWidth     = 60
)

func init() {
	r = mux.NewRouter()
	Users.List = make(map[string]*Player)
	Rooms.List = map[string]*Room{}
	mainRoom := RoomInit("123")
	go mainRoom.Handle()
	Rooms.Add(mainRoom)
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
