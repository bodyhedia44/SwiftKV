package main

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

func initHandShake(conn net.Conn, port string, sharedState *RedisState) {
	// Send PING
	conn.Write([]byte("*1\r\n$4\r\nPING\r\n"))
	waitForSimpleResponse(conn) // Wait for +PONG

	// Send REPLCONF listening-port-
	conn.Write([]byte(fmt.Sprintf("*3\r\n$8\r\nREPLCONF\r\n$14\r\nlistening-port\r\n$%d\r\n%s\r\n", len(port), port)))
	waitForSimpleResponse(conn)

	// Send REPLCONF capa psync2
	conn.Write([]byte("*3\r\n$8\r\nREPLCONF\r\n$4\r\ncapa\r\n$6\r\npsync2\r\n"))
	waitForSimpleResponse(conn)

	// Send PSYNC
	conn.Write([]byte("*3\r\n$5\r\nPSYNC\r\n$1\r\n?\r\n$2\r\n-1\r\n"))

	// Handle FULLRESYNC response only
	waitForFullResyncResponse(conn)

	// Create replica server to handle the master stream including RDB and commands
	redisServer := RedisServer{
		state:      sharedState,
		conn:       conn,
		ReplOffset: 0,
	}
	go redisServer.handleMasterStream()
}

func waitForSimpleResponse(conn net.Conn) {
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		fmt.Println("Error reading from master:", err)
		return
	}
	response := string(buf[:n])
	lines := strings.Split(response, "\r\n")
	if len(lines) > 0 && (strings.HasPrefix(lines[0], "+") || strings.HasPrefix(lines[0], "-")) {
		fmt.Println("Received from master:", lines[0])
	}
}

func waitForFullResyncResponse(conn net.Conn) {
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		fmt.Println("Error reading FULLRESYNC response:", err)
		return
	}
	response := string(buf[:n])
	lines := strings.Split(response, "\r\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "+FULLRESYNC") {
		fmt.Println("Received from master:", lines[0])
	}
}

func (s *RedisServer) handleMasterStream() {
	defer s.conn.Close()
	fmt.Println("Starting to listen for replication commands...")

	buffer := make([]byte, 4096)
	accumulated := ""
	rdbConsumed := false
	rdbLength := -1
	rdbBytesConsumed := 0

	for {
		n, err := s.conn.Read(buffer)
		if err != nil {
			if err == io.EOF {
				fmt.Printf("Master connection closed\n")
			} else {
				fmt.Printf("Error reading from master: %v\n", err)
			}
			return
		}

		accumulated += string(buffer[:n])

		// First consume RDB data if not done yet
		if !rdbConsumed {
			// Parse RDB header $<length>\r\n
			if rdbLength == -1 && strings.HasPrefix(accumulated, "$") {
				endPos := strings.Index(accumulated, "\r\n")
				if endPos != -1 {
					lengthStr := accumulated[1:endPos]
					var err error
					rdbLength, err = strconv.Atoi(lengthStr)
					if err == nil {
						// Remove RDB header
						accumulated = accumulated[endPos+2:]
						rdbBytesConsumed = 0
					}
				}
			}

			// Consume RDB data bytes
			if rdbLength > 0 {
				bytesNeeded := rdbLength - rdbBytesConsumed
				if len(accumulated) >= bytesNeeded {
					// Have all RDB data, consume it
					accumulated = accumulated[bytesNeeded:]
					rdbBytesConsumed = rdbLength
					rdbConsumed = true
					fmt.Printf("RDB data received and consumed (%d bytes)\n", rdbLength)
				} else {
					// Consume all available and continue
					rdbBytesConsumed += len(accumulated)
					accumulated = ""
					continue
				}
			}
		}

		// Process RESP commands after RDB is consumed
		if rdbConsumed {
			for len(accumulated) > 0 {
				// Parse RESP array
				if !strings.HasPrefix(accumulated, "*") {
					// Find next RESP command
					starPos := strings.Index(accumulated[1:], "*")
					if starPos == -1 {
						accumulated = ""
						break
					}
					accumulated = accumulated[starPos+1:]
					continue
				}

				// Get array length
				crlfPos := strings.Index(accumulated, "\r\n")
				if crlfPos == -1 {
					break // Need more data
				}

				arrayLenStr := accumulated[1:crlfPos]
				arrayLen, err := strconv.Atoi(arrayLenStr)
				if err != nil {
					accumulated = accumulated[crlfPos+2:]
					continue
				}

				// Parse array elements
				pos := crlfPos + 2
				elements := make([]string, 0, arrayLen)

				for i := 0; i < arrayLen; i++ {
					if pos >= len(accumulated) || accumulated[pos] != '$' {
						goto needMoreData
					}

					// Get bulk string length
					bulkCrlfPos := strings.Index(accumulated[pos:], "\r\n")
					if bulkCrlfPos == -1 {
						goto needMoreData
					}
					bulkCrlfPos += pos

					bulkLenStr := accumulated[pos+1 : bulkCrlfPos]
					bulkLen, err := strconv.Atoi(bulkLenStr)
					if err != nil {
						goto skipCommand
					}

					pos = bulkCrlfPos + 2

					// Get bulk string data
					if pos+bulkLen+2 > len(accumulated) {
						goto needMoreData
					}

					element := accumulated[pos : pos+bulkLen]
					elements = append(elements, element)
					pos += bulkLen + 2
				}

				// Process complete command
				s.processReplicationCommand(elements)
				accumulated = accumulated[pos:]
				continue

			needMoreData:
				break
			skipCommand:
				accumulated = accumulated[pos:]
				break
			}
		}
	}
}

func (s *RedisServer) processReplicationCommand(cmd []string) {
	if len(cmd) == 0 {
		return
	}

	command := strings.ToUpper(cmd[0])
	fmt.Printf("Processing replication command: %v\n", cmd)

	switch command {
	case "REPLCONF":
		if len(cmd) >= 3 && strings.ToUpper(cmd[1]) == "GETACK" && cmd[2] == "*" {
			// Send REPLCONF ACK 0
			// fmt.Println(s.ReplOffset)
			ack := "*3\r\n$8\r\nREPLCONF\r\n$3\r\nACK\r\n$1\r\n0\r\n"
			_, err := s.conn.Write([]byte(ack))

			// fmt.Println(toRespArr("REPLCONF", "ACK", strconv.Itoa(s.ReplOffset)))
			// _, err := s.conn.Write([]byte(toRespArr("REPLCONF", "ACK", strconv.Itoa(s.ReplOffset))))

			// ack := strconv.FormatInt(int64(s.ReplOffset), 10)
			// _, err := fmt.Fprintf(s.conn, "*3\r\n$8\r\nREPLCONF\r\n$3\r\nACK\r\n$%d\r\n%s\r\n", len(ack), ack)

			if err != nil {
				fmt.Println("Error sending REPLCONF ACK:", err)
			} else {
				fmt.Println("Sent REPLCONF ACK " + strconv.Itoa(s.ReplOffset))
			}
		}
	case "SET":
		if len(cmd) < 3 {
			return
		}

		var val storageVal
		if len(cmd) > 4 && strings.ToUpper(cmd[3]) == "PX" {
			px, err := strconv.Atoi(cmd[4])
			if err != nil {
				px = -1
			}
			val = storageVal{val: cmd[2], px: px, t: time.Now()}
		} else {
			val = storageVal{val: cmd[2], px: -1, t: time.Now()}
		}

		s.state.storage[cmd[1]] = val
		fmt.Printf("Replica SET: %s = %s\n", cmd[1], cmd[2])

	case "DEL":
		if len(cmd) >= 2 {
			delete(s.state.storage, cmd[1])
			fmt.Printf("Replica DEL: %s\n", cmd[1])
		}

	case "PING":
		fmt.Println("Received PING from master")

	default:
		fmt.Printf("Unhandled replication command: %s\n", command)
	}
}
