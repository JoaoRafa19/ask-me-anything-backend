package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/JoaoRafa19/ask-me-anything-backend/internal/store/pgstore"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
)

func (h apiHandler) handleEcho(w http.ResponseWriter, r *http.Request) {
	message := chi.URLParam(r, "message")
	w.Write([]byte(message))
}

func (h apiHandler) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	rawRoomId := chi.URLParam(r, "room_id")
	roomId, err := uuid.Parse(rawRoomId)
	if err != nil {
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	_, err = h.q.GetRoom(r.Context(), roomId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "room not found", http.StatusBadRequest)
			return
		}
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	}

	c, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("failed to upgrade connection", "error", err)
		http.Error(w, "failed to upgrade to ws connection", http.StatusBadRequest)
		return
	}

	defer c.Close()
	ctx, cancel := context.WithCancel(r.Context())
	h.mu.Lock()
	if _, ok := h.subscribers[rawRoomId]; !ok {
		h.subscribers[rawRoomId] = make(map[*websocket.Conn]context.CancelFunc)
	}
	slog.Info("new client connected", "room_id", rawRoomId, "client_ip", r.RemoteAddr)
	h.subscribers[rawRoomId][c] = cancel
	h.mu.Unlock()

	<-ctx.Done()

	h.mu.Lock()
	delete(h.subscribers[rawRoomId], c)
	h.mu.Unlock()
}

func (h apiHandler) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	slog.Info("Create new room")
	type _body struct {
		Theme string `json:"theme"`
	}

	var body _body

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if len(body.Theme) <= 0 || body.Theme == "" {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	roomId, err := h.q.InsertRoom(r.Context(), body.Theme)
	if err != nil {
		slog.Error("failed to insert room", "error", err)

		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	}

	type response struct {
		ID string `json:"id"`
	}
	data, _ := json.Marshal(response{ID: roomId.String()})
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(data)
	if err != nil {
		slog.Error("failed to return response room", "error", err)
	}
}

func (h apiHandler) handleCreateRoomMessages(w http.ResponseWriter, r *http.Request) {
	slog.Info("Create new message")
	rawRoomId := chi.URLParam(r, "room_id")
	roomId, err := uuid.Parse(rawRoomId)
	if err != nil {
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	_, err = h.q.GetRoom(r.Context(), roomId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "room not found", http.StatusBadRequest)
			return
		}
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	}

	type _body struct {
		Message string `json:"message"`
	}

	var body _body

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if len(body.Message) <= 0 || body.Message == "" {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	messageID, err := h.q.InsertMessage(r.Context(), pgstore.InsertMessageParams{
		RoomID:  roomId,
		Message: body.Message,
	})

	if err != nil {
		slog.Error("failed to send message", "error", err)
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	}

	type response struct {
		ID string `json:"id"`
	}
	data, _ := json.Marshal(response{ID: messageID.String()})
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(data)
	if err != nil {
		slog.Error("failed to return response room", "error", err)
	}

	go h.notifyClients(Message{
		Kind:   MessageKindMessageCreated,
		RoomID: rawRoomId,
		Value: MessageMessageCreated{
			ID:      messageID.String(),
			Message: body.Message,
		}},
	)
}

func (h apiHandler) handleReactToMessage(w http.ResponseWriter, r *http.Request) {
	slog.Info("React to message")

	var rawRoomId uuid.UUID

	roomId := chi.URLParam(r, "room_id")

	rawRoomId, err := uuid.Parse(roomId)
	if err != nil {
		http.Error(w, "Invalid room id", http.StatusBadRequest)
		slog.Error(fmt.Sprintf("invalid room id? %s", rawRoomId.String()), "error", err.Error())
		return
	}

	var rawMessageId uuid.UUID

	messageId := chi.URLParam(r, "message_id")

	rawMessageId, err = uuid.Parse(messageId)
	if err != nil {
		http.Error(w, "Invalid message id", http.StatusBadRequest)
		slog.Error(fmt.Sprintf("invalid message id? %s", rawMessageId.String()), "error", err.Error())
		return
	}
	slog.Info("messageID", rawMessageId.String(), "error")
	id, err := h.q.ReactToMessage(r.Context(), rawMessageId)
	if err != nil {
		slog.Error(fmt.Sprintf("error to react to message %d", id), "error", err.Error())
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}

	go h.notifyClients(Message{
		Kind:   MessageKindMessageReaction,
		RoomID: rawRoomId.String(),
		Value: MessageMessageReacted{
			ID: rawMessageId.String(),
		}},
	)
}

func (apiHandler) getIdParam(param string, r *http.Request) (uuid.UUID, error) {
	rawParam := chi.URLParam(r, param)
	id, err := uuid.Parse(rawParam)
	if err != nil {
		slog.Error("parse id", "error", err)
		return uuid.Nil, fmt.Errorf("invalid param %s", param)
	}
	return id, nil
}

func (h apiHandler) handleRemoveMessageReaction(w http.ResponseWriter, r *http.Request) {
	slog.Info("React to message")

	var rawRoomId uuid.UUID

	roomId := chi.URLParam(r, "room_id")

	rawRoomId, err := uuid.Parse(roomId)
	if err != nil {
		http.Error(w, "Invalid room id", http.StatusBadRequest)
		slog.Error(fmt.Sprintf("invalid room id? %s", rawRoomId.String()), "error", err.Error())
		return
	}

	var rawMessageId uuid.UUID

	messageId := chi.URLParam(r, "message_id")

	rawMessageId, err = uuid.Parse(messageId)
	if err != nil {
		http.Error(w, "Invalid message id", http.StatusBadRequest)
		slog.Error(fmt.Sprintf("invalid message id? %s", rawMessageId.String()), "error", err.Error())
		return
	}
	slog.Info("messageID", rawMessageId.String(), "error")
	id, err := h.q.RemoveReactionFromMessage(r.Context(), rawMessageId)
	if err != nil {
		slog.Error(fmt.Sprintf("error to react to message %d", id), "error", err.Error())
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}

	go h.notifyClients(Message{
		Kind:   MessageKindMessageReaction,
		RoomID: rawRoomId.String(),
		Value: MessageMessageReacted{
			ID: rawMessageId.String(),
		}},
	)
}

func (h apiHandler) handleMarkMessageAsAnswered(w http.ResponseWriter, r *http.Request) {
	slog.Info("Mark message as awnsered")

	var rawRoomId uuid.UUID

	if rawRoomId, err := h.getIdParam("room_id", r); err != nil {
		http.Error(w, "Invalid room id", http.StatusBadRequest)
		slog.Error(fmt.Sprintf("invalid room id? %s", rawRoomId.String()), "error", err.Error())
		return
	}

	var messageId uuid.UUID

	if messageId, err := h.getIdParam("message_id", r); err != nil {
		slog.Error(fmt.Sprintf("invalid room id? %s", messageId.String()), "error", err.Error())
		http.Error(w, "Invalid message id", http.StatusBadRequest)
		return
	}

	h.q.MarkMessageAsAwnsered(r.Context(), messageId)

	go h.notifyClients(Message{
		Kind:   MessageKindMessageReaction,
		RoomID: rawRoomId.String(),
		Value: MessageMessageReacted{
			ID: messageId.String(),
		}},
	)
}

func (h apiHandler) handleGetRooms(w http.ResponseWriter, r *http.Request) {
	rooms, err := h.q.GetRooms(r.Context())

	if err != nil {
		http.Error(w, "failed get rooms", http.StatusBadRequest)
		return
	}

	body, err := json.Marshal(rooms)
	if err != nil {
		slog.Error("erro ao buscar salas", "error", err)
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	}

	w.Write(body)
}
func (h apiHandler) handleGetRoomMessages(w http.ResponseWriter, r *http.Request) {
	var rawRoomId uuid.UUID

	rawRoomId, err := h.getIdParam("room_id", r)
	if err != nil {
		http.Error(w, "Invalid room id", http.StatusBadRequest)
		slog.Error(fmt.Sprintf("invalid room id? %s", rawRoomId.String()), "error", err.Error())
		return
	}

	messages, err := h.q.GetRoomMessages(r.Context(), rawRoomId)
	if err != nil {
		http.Error(w, "failed to get messages", http.StatusBadRequest)
		slog.Error(fmt.Sprintf("failed to get messages from %s", rawRoomId.String()), "error", err.Error())
		return
	}

	jsonMessages, err := json.Marshal(messages)
	if err != nil {
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		slog.Error(fmt.Sprintf("failed to get messages from %s", rawRoomId.String()), "error", err.Error())
		return
	}

	w.Write(jsonMessages)
}

func (h apiHandler) handleGetRoomMessage(w http.ResponseWriter, r *http.Request) {
	var rawRoomId uuid.UUID

	rawRoomId, err := h.getIdParam("room_id", r)
	if err != nil {
		http.Error(w, "Invalid room id", http.StatusBadRequest)
		slog.Error(fmt.Sprintf("invalid room id? %s", rawRoomId.String()), "error", err.Error())
		return
	}

	var rawMessageId uuid.UUID

	messageId := chi.URLParam(r, "message_id")

	rawMessageId, err = uuid.Parse(messageId)
	if err != nil {
		http.Error(w, "Invalid message id", http.StatusBadRequest)
		slog.Error(fmt.Sprintf("invalid message id? %s", rawMessageId.String()), "error", err.Error())
		return
	}

	messages, err := h.q.GetMessage(r.Context(), rawMessageId)
	if err != nil {
		http.Error(w, "failed to get messages", http.StatusBadRequest)
		slog.Error(fmt.Sprintf("failed to get messages from %s", rawRoomId.String()), "error", err.Error())
		return
	}

	jsonMessages, err := json.Marshal(messages)
	if err != nil {
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		slog.Error(fmt.Sprintf("failed to get messages from %s", rawRoomId.String()), "error", err.Error())
		return
	}

	w.Write(jsonMessages)
}
