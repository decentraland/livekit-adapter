package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/spf13/viper"
	"gopkg.in/dealancer/validate.v2"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"google.golang.org/protobuf/proto"

	lksdk "github.com/livekit/server-sdk-go"

	"github.com/golang-jwt/jwt/v4"
)

type Config struct {
	RegistrationURL    string `validate:"empty=false & format=url"`
	RegistrationSecret string `validate:"empty=false"`
	LivekitApiKey      string `validate:"empty=false"`
	LivekitApiSecret   string `validate:"empty=false"`
	LivekitHost        string `validate:"empty=false & format=url"`

	MaxUsers      uint32 `validate:"gt=0"`
	MaxIslandSize uint32 `validate:"gt=0"`
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

	config := Config{
		RegistrationURL:    viper.GetString("TRANSPORT_REGISTRATION_URL"),
		RegistrationSecret: viper.GetString("TRANSPORT_REGISTRATION_SECRET"),
		LivekitHost:        viper.GetString("LIVEKIT_HOST"),
		MaxUsers:           viper.GetUint32("MAX_USERS"),
		MaxIslandSize:      viper.GetUint32("MAX_ISLAND_SIZE"),
		LivekitApiKey:      viper.GetString("LIVEKIT_API_KEY"),
		LivekitApiSecret:   viper.GetString("LIVEKIT_API_SECRET"),
	}

	if err := validate.Validate(&config); err != nil {
		s, _ := json.MarshalIndent(config, "", "\t")
		log.Err(err).Msgf("Config is invalid %s", string(s))
		return
	}

	roomClient := lksdk.NewRoomServiceClient(config.LivekitHost, config.LivekitApiKey, config.LivekitApiSecret)

	getParticipantsCount := func() (uint32, error) {
		rooms, err := roomClient.ListRooms(context.Background(), &livekit.ListRoomsRequest{})
		if err != nil {
			return 0, err
		}

		participants := 0
		for _, room := range rooms.GetRooms() {
			participants += int(room.NumParticipants)
		}
		return uint32(participants), nil
	}

	participants, err := getParticipantsCount()
	if err != nil {
		log.Err(err).Msg("Error fetching rooms")
		return
	}
	log.Debug().Msgf("livekit has %d participants", participants)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{})
	accessToken, err := token.SignedString([]byte(config.RegistrationSecret))
	if err != nil {
		log.Err(err).Msg("Error creating jwt")
		return
	}
	registrationURL := fmt.Sprintf("%s?access_token=%s", config.RegistrationURL, accessToken)

	ws, _, err := websocket.DefaultDialer.Dial(registrationURL, nil)
	if err != nil {
		log.Err(err).Msg("error connecting to transport registration, going to retry once")

		time.Sleep(5 * time.Second)
		ws, _, err = websocket.DefaultDialer.Dial(registrationURL, nil)
		if err != nil {
			log.Err(err).Msg("error connecting to transport registration, stop")
			return
		}
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
			usersCount, err := getParticipantsCount()
			if err != nil {
				log.Err(err).Msg("Error getting livekit participants")
				return
			}

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
