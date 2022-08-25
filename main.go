package main

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/spf13/viper"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"google.golang.org/protobuf/proto"
	"gopkg.in/dealancer/validate.v2"

	lksdk "github.com/livekit/server-sdk-go"
)

type Config struct {
	RegistrationURL  string `validate:"empty=false & format=url" mapstructure:"TRANSPORT_REGISTRATION_URL"`
	LivekitApiKey    string `validate:"empty=false" mapstructure:"LIVEKIT_API_KEY"`
	LivekitApiSecret string `validate:"empty=false" mapstructure:"LIVEKIT_API_SECRET"`
	LivekitHost      string `validate:"empty=false & format=url" mapstructure:"LIVEKIT_HOST"`

	MaxUsers      uint32 `validate:"gt=0" mapstructure:"MAX_USERS"`
	MaxIslandSize uint32 `validate:"gt=0" mapstructure:"MAX_ISLAND_SIZE"`
}

func generateConnStrs(config *Config, room string, userIds []string) (map[string]string, error) {
	connStrs := make(map[string]string)
	for i := 0; i < len(userIds); i++ {
		userId := userIds[i]
		at := auth.NewAccessToken(config.LivekitApiKey, config.LivekitApiSecret)
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

		connStrs[userId] = fmt.Sprintf("livekit:%s?access_token=%s", config.LivekitHost, jwt)
	}

	return connStrs, nil
}

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	viper.SetDefault("MAX_USERS", 100)
	viper.SetDefault("MAX_ISLAND_SIZE", 50)

	viper.SetConfigName(".env")
	viper.SetConfigType("env")
	viper.AddConfigPath(".")
	viper.AutomaticEnv()

	err := viper.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			log.Err(err).Msg("Error reading config file")
			return
		}
	}

	config := Config{}
	if err := viper.Unmarshal(&config); err != nil {
		log.Err(err).Msg("Error unmarshalling config")
		return
	}

	if err := validate.Validate(&config); err != nil {
		log.Err(err).Msg("Config is invalid")
		return
	}

	roomClient := lksdk.NewRoomServiceClient(config.LivekitHost, config.LivekitApiKey, config.LivekitApiSecret)
	res, err := roomClient.ListParticipants(context.Background(), &livekit.ListParticipantsRequest{})
	if err != nil {
		log.Err(err).Msg("Error connecting to livekit")
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
					MaxIslandSize: config.MaxIslandSize,
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

				connStrs, err := generateConnStrs(&config, roomId, userIds)
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
							AvailableSeats: config.MaxUsers - usersCount,
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
