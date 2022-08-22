package main

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"google.golang.org/protobuf/proto"

	lksdk "github.com/livekit/server-sdk-go"
)

type LivekitConfig struct {
	ApiKey        string
	ApiSecret     string
	Host          string
	MaxUsers      uint32
	MaxIslandSize uint32
}

type Config struct {
	Livekit         LivekitConfig
	RegistrationURL string
}

func generateConnStrs(config *LivekitConfig, room string, userIds []string) (map[string]string, error) {
	connStrs := make(map[string]string)
	for i := 0; i < len(userIds); i++ {
		userId := userIds[i]
		at := auth.NewAccessToken(config.ApiKey, config.ApiSecret)
		grant := &auth.VideoGrant{
			RoomJoin: true,
			Room:     room,
		}
		at.AddGrant(grant).
			SetIdentity(userId).
			SetValidFor(time.Hour)

		jwt, err := at.ToJWT()
		if err != nil {
			return nil, err
		}

		connStrs[userId] = fmt.Sprintf("livekit:%s?access_token=%s", config.Host, jwt)
	}

	return connStrs, nil
}

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	config := Config{
		Livekit: LivekitConfig{
			Host:          "http://127.0.0.1:7880",
			ApiKey:        "TEST_KEY",
			ApiSecret:     "TEST_SECRET",
			MaxUsers:      100,
			MaxIslandSize: 50,
		},
		RegistrationURL: "ws://localhost:5002/transport-registration",
	}

	roomClient := lksdk.NewRoomServiceClient(config.Livekit.Host, config.Livekit.ApiKey, config.Livekit.ApiSecret)
	res, err := roomClient.ListParticipants(context.Background(), &livekit.ListParticipantsRequest{})
	if err != nil {
		log.Err(err).Msg("error connecting to livekit")
		return
	}

	log.Debug().Msgf("livekit has %d participants", len(res.Participants))

	ws, _, err := websocket.DefaultDialer.Dial(config.RegistrationURL, nil)
	if err != nil {
		log.Err(err).Msg("error connecting to transport registration")
		return
	}

	defer ws.Close()

	data, err := proto.Marshal(
		&TransportMessage{
			Message: &TransportMessage_Init{
				Init: &TransportInit{
					Type:          TransportType_TRANSPORT_LIVEKIT,
					MaxIslandSize: config.Livekit.MaxIslandSize,
				},
			},
		})
	if err != nil {
		log.Err(err).Msg("Error encoding message init")
		return
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
		log.Err(err).Msg("Error writing init message")
		return
	}

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				log.Err(err).Msg("Error reading from socket")
				return
			}

			message := &TransportMessage{}
			if err = proto.Unmarshal(msg, message); err != nil {
				log.Err(err).Msgf("Error decoding message")
				continue
			}

			log.Debug().Msg("got new message")

			switch m := message.Message.(type) {
			case *TransportMessage_AuthRequest:
				log.Debug().Msg("got auth request message")
				requestId := m.AuthRequest.GetRequestId()
				roomId := m.AuthRequest.GetRoomId()
				userIds := m.AuthRequest.GetUserIds()

				connStrs, err := generateConnStrs(&config.Livekit, roomId, userIds)
				if err != nil {
					log.Err(err).Msg("Error generating connection strings")
					continue
				}

				data, err := proto.Marshal(
					&TransportMessage{
						Message: &TransportMessage_AuthResponse{
							AuthResponse: &TransportAuthorizationResponse{
								RequestId: requestId,
								ConnStrs:  connStrs,
							},
						},
					})
				if err != nil {
					log.Err(err).Msg("Error encoding auth reponse message")
					continue
				}
				if err := ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
					log.Err(err).Msg("Error writing auth response message")
					continue
				}

				log.Debug().Msg("auth response sent")
			default:
				log.Warn().Msg("Unhandled message type")
			}
		}
	}()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	log.Info().Msg("Connected")
	for {
		select {
		case <-ticker.C:
			res, err := roomClient.ListParticipants(context.Background(), &livekit.ListParticipantsRequest{})
			if err != nil {
				log.Err(err).Msg("Error getting livekit participants")
				return
			}

			usersCount := uint32(len(res.Participants))

			log.Debug().Msgf("livekit has %d participants", usersCount)

			data, err := proto.Marshal(
				&TransportMessage{
					Message: &TransportMessage_Heartbeat{
						Heartbeat: &TransportHeartbeat{
							AvailableSeats: config.Livekit.MaxUsers - usersCount,
							UsersCount:     usersCount,
						},
					},
				})
			if err != nil {
				log.Fatal().Err(err).Msg("Error encoding heartbeat message")
			}
			if err := ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
				log.Fatal().Err(err).Msg("Error writing heartbeat message")
			}
		case <-done:
			return
		}
	}

}
