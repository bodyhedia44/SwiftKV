package main

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

type storageVal struct {
	val interface{}
	// val string
	px int
	t  time.Time
}

// type storageVal[T any] struct {
// 	val T
// 	px  int
// 	t   time.Time
// }

type Channel struct {
	name        string
	connections []*net.Conn
	mu          sync.RWMutex
}

type Config struct {
	Directory  string
	dbFileName string
}

// Global Redis server state
type RedisState struct {
	storage        map[string]storageVal
	config         Config
	serverIsMaster bool
	replicaConns   []net.Conn
	channels       map[string]Channel
	storageMu      sync.RWMutex
	replicaMu      sync.RWMutex
	channelsMu     sync.RWMutex
}

type RedisServer struct {
	state          *RedisState
	conn           net.Conn
	ReplOffset     int
	MultiOn        bool
	multiQueue     [][]string
	SubscribedMode bool
}

func (s *RedisServer) handleConnection() {
	defer s.conn.Close()

	buffer := make([]byte, 1024)

	for {
		n, err := s.conn.Read(buffer)

		if err != nil {
			if err == io.EOF {
				fmt.Printf("Client %s disconnected.\n", s.conn.RemoteAddr())
			} else {
				fmt.Printf("Error reading from %s: %v\n", s.conn.RemoteAddr(), err)
			}
			return
		}

		tempArr, err := RESPToArray(string(buffer[:n]))
		if err != nil {
			s.conn.Write([]byte("-ERR invalid RESP format\r\n"))
			continue
		}
		if len(tempArr) == 0 {
			s.conn.Write([]byte("-ERR empty command\r\n"))
			continue
		}

		cmd := strings.ToUpper(tempArr[0])

		// Handle transaction commands
		if cmd == RESP_COMMAND_MULTI {
			s.MultiOn = true
			s.conn.Write([]byte("+OK\r\n"))
			continue
		}

		if cmd == RESP_COMMAND_DISCARD {
			if !s.MultiOn {
				s.conn.Write([]byte("-ERR DISCARD without MULTI\r\n"))
			} else {
				s.MultiOn = false
				s.conn.Write([]byte("+OK\r\n"))
				s.multiQueue = nil
			}
			continue
		}

		if cmd == RESP_COMMAND_EXEC {
			if !s.MultiOn {
				s.conn.Write([]byte("-ERR EXEC without MULTI\r\n"))
			} else {
				s.MultiOn = false

				responses := make([]string, len(s.multiQueue))

				for i, queuedCmd := range s.multiQueue {
					cmdResponse := s.executeCommand(queuedCmd)
					if cmdResponse.Error != "" {
						responses[i] = cmdResponse.Error
					} else {
						responses[i] = cmdResponse.Response
					}
				}

				response := fmt.Sprintf("*%d\r\n", len(responses))
				for _, resp := range responses {
					if !strings.HasSuffix(resp, "\r\n") {
						resp += "\r\n"
					}
					response += resp
				}

				s.conn.Write([]byte(response))

				s.multiQueue = nil
			}
			continue
		}

		if s.MultiOn {
			if cmd == "MULTI" {
				s.conn.Write([]byte("-ERR MULTI calls can not be nested\r\n"))
				continue
			}
			s.multiQueue = append(s.multiQueue, tempArr)
			s.conn.Write([]byte("+QUEUED\r\n"))
			continue
		}

		cmdResponse := s.executeCommand(tempArr)

		if cmdResponse.Error != "" {
			s.conn.Write([]byte(cmdResponse.Error + "\r\n"))
		} else if cmdResponse.Response != "" {
			if s.state.serverIsMaster || cmd != RESP_COMMAND_SET {
				if !strings.HasSuffix(cmdResponse.Response, "\r\n") {
					cmdResponse.Response += "\r\n"
				}
				s.conn.Write([]byte(cmdResponse.Response))
			}
		}
	}
}
