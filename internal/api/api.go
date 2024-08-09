package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/JoaoRafa19/ask-me-anything-backend/internal/store/pgstore"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/gorilla/websocket"
)

const (
	MessageKindMessageCreated  = "message_created"
	MessageKindMessageReaction = "message_reaction"
)

type MessageMessageCreated struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}



type MessageMessageReacted struct {
	ID string `json:"id"`
}

type Message struct {
	Kind   string `json:"kind"`
	Value  any    `json:"value"`
	RoomID string `json:"-"`
}

type apiHandler struct {
	q           *pgstore.Queries
	r           *chi.Mux
	upgrader    websocket.Upgrader
	subscribers map[string]map[*websocket.Conn]context.CancelFunc
	mu          *sync.Mutex
}

func (h apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.r.ServeHTTP(w, r)
}

func NewHandler(q *pgstore.Queries) http.Handler {
	h := apiHandler{
		q: q,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		mu:          &sync.Mutex{},
		subscribers: make(map[string]map[*websocket.Conn]context.CancelFunc),
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, middleware.Logger)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	r.Get("/echo/{message}", h.handleEcho)
	r.Get("/subscribe/{room_id}", h.handleSubscribe)
	r.Route("/api", func(r chi.Router) {
		r.Route("/rooms", func(r chi.Router) {
			r.Post("/", h.handleCreateRoom)
			r.Get("/", h.handleGetRooms)
			r.Route("/{room_id}/messages", func(r chi.Router) {
				r.Post("/", h.handleCreateRoomMessages)
				r.Get("/", h.handleGetRoomMessages)
				r.Route("/{message_id}", func(r chi.Router) {
					r.Get("/", h.handleGetRoomMessage)
					r.Patch("/react", h.handleReactToMessage)
					r.Delete("/react", h.handleRemoveMessageReaction)
					r.Patch("/answer", h.handleMarkMessageAsAnswered)
				})
			})
		})
	})
	h.r = r
	return h
}

func (h apiHandler) notifyClients(msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()

	subs, ok := h.subscribers[msg.RoomID]
	if !ok || len(subs) == 0 {
		return
	}

	for conn, cancel := range subs {
		if err := conn.WriteJSON(msg); err != nil {
			slog.Error("failed to send message to client", "error", err)
			cancel()
		}
	}
}
