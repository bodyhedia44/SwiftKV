package main

import (
	"encoding/hex"
	"fmt"
	"net"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/wangjia184/sortedset"
)

type CommandResponse struct {
	Response string
	Error    string
}

const (
	RESP_COMMAND_UNKNOWN     string = "UNKNOWN"
	RESP_COMMAND_PING        string = "PING"
	RESP_COMMAND_ECHO        string = "ECHO"
	RESP_COMMAND_SET         string = "SET"
	RESP_COMMAND_GET         string = "GET"
	RESP_COMMAND_CONFIG      string = "CONFIG"
	RESP_COMMAND_KEYS        string = "KEYS"
	RESP_COMMAND_INFO        string = "INFO"
	RESP_COMMAND_REPLCONF    string = "REPLCONF"
	RESP_COMMAND_PSYNC       string = "PSYNC"
	RESP_COMMAND_TYPE        string = "TYPE"
	RESP_COMMAND_INCR        string = "INCR"
	RESP_COMMAND_MULTI       string = "MULTI"
	RESP_COMMAND_EXEC        string = "EXEC"
	RESP_COMMAND_DISCARD     string = "DISCARD"
	RESP_COMMAND_RPUSH       string = "RPUSH"
	RESP_COMMAND_LRANGE      string = "LRANGE"
	RESP_COMMAND_LPUSH       string = "LPUSH"
	RESP_COMMAND_LPOP        string = "LPOP"
	RESP_COMMAND_LLEN        string = "LLEN"
	RESP_COMMAND_SUBSCRIBE   string = "SUBSCRIBE"
	RESP_COMMAND_PUBLISH     string = "PUBLISH"
	RESP_COMMAND_UNSUBSCRIBE string = "UNSUBSCRIBE"
	RESP_COMMAND_ZADD        string = "ZADD"
	RESP_COMMAND_ZRANK       string = "ZRANK"
	RESP_COMMAND_ZRANGE      string = "ZRANGE"
	RESP_COMMAND_ZCARD       string = "ZCARD"
	RESP_COMMAND_ZSCORE      string = "ZSCORE"
	RESP_COMMAND_ZREM        string = "ZREM"
)

func (s *RedisServer) executeCommand(tempArr []string) CommandResponse {
	if len(tempArr) == 0 {
		return CommandResponse{Error: "-ERR empty command"}
	}

	cmd := strings.ToUpper(tempArr[0])

	if s.SubscribedMode {
		allowed := map[string]bool{
			"SUBSCRIBE":    true,
			"UNSUBSCRIBE":  true,
			"PSUBSCRIBE":   true,
			"PUNSUBSCRIBE": true,
			"PING":         true,
			"QUIT":         true,
		}

		if !allowed[cmd] {
			return CommandResponse{
				Error: fmt.Sprintf(
					"-ERR Can't execute '%s': only (P|S)SUBSCRIBE / (P|S)UNSUBSCRIBE / PING / QUIT / RESET are allowed in this context",
					strings.ToLower(cmd),
				),
			}
		}
	}

	switch cmd {
	case RESP_COMMAND_PING:
		if s.SubscribedMode {
			resp := "*2\r\n" +
				"$4\r\npong\r\n" +
				"$0\r\n\r\n"
			return CommandResponse{Response: resp}
		} else {
			return CommandResponse{Response: "+PONG\r\n"}
		}

	case RESP_COMMAND_ECHO:
		if len(tempArr) < 2 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'echo' command"}
		}
		tempStr := tempArr[1]
		for i := 2; i < len(tempArr); i++ {
			tempStr += " " + tempArr[i]
		}
		response := fmt.Sprintf("$%d\r\n%s", len(tempStr), tempStr)
		return CommandResponse{Response: response}

	case RESP_COMMAND_SET:
		if len(tempArr) < 3 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'SET' command"}
		}
		var val storageVal

		if len(tempArr) > 3 && strings.ToUpper(tempArr[3]) == "PX" {
			px, _ := strconv.Atoi(tempArr[4])
			val = storageVal{val: tempArr[2], px: px, t: time.Now()}
		} else {
			val = storageVal{val: tempArr[2], px: -1, t: time.Now()}
		}
		s.state.storageMu.Lock()
		s.state.storage[tempArr[1]] = val
		s.state.storageMu.Unlock()

		s.state.replicaMu.RLock()
		for _, replica := range s.state.replicaConns {
			replica.Write(encodeBulkArray(tempArr))
		}
		s.state.replicaMu.RUnlock()

		return CommandResponse{Response: "+OK"}

	case RESP_COMMAND_GET:
		if len(tempArr) < 2 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'GET' command"}
		}
		s.state.storageMu.RLock()
		value, ok := s.state.storage[tempArr[1]]
		s.state.storageMu.RUnlock()
		if ok {
			if value.px != -1 &&
				time.Now().After(value.t.Add(time.Millisecond*time.Duration(value.px))) {
				s.state.storageMu.Lock()
				delete(s.state.storage, tempArr[1])
				s.state.storageMu.Unlock()
				return CommandResponse{Response: "$-1"}
			} else {
				resp := fmt.Sprintf("$%d\r\n%s", len(value.val.(string)), value.val)
				return CommandResponse{Response: resp}
			}
		} else {
			return CommandResponse{Response: "$-1"}
		}

	case RESP_COMMAND_CONFIG:
		if len(tempArr) < 3 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'CONFIG' command"}
		}
		if strings.ToUpper(tempArr[1]) == "GET" && strings.ToUpper(tempArr[2]) == "DIR" {
			resp := fmt.Sprintf("*2\r\n$3\r\ndir\r\n$%d\r\n%s", len(s.state.config.Directory), s.state.config.Directory)
			return CommandResponse{Response: resp}
		}
		if strings.ToUpper(tempArr[1]) == "GET" && strings.ToUpper(tempArr[2]) == "DBFILENAME" {
			dbFileName := s.state.config.dbFileName
			if dbFileName == "" {
				dbFileName = "dump.rdb"
			}
			resp := fmt.Sprintf("*2\r\n$10\r\ndbfilename\r\n$%d\r\n%s", len(dbFileName), dbFileName)
			return CommandResponse{Response: resp}
		}
		return CommandResponse{Error: "-ERR unsupported CONFIG subcommand"}

	case RESP_COMMAND_KEYS:
		if len(tempArr) < 2 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'KEYS' command"}
		}
		if tempArr[1] == "*" {
			filePath := path.Join(s.state.config.Directory, s.state.config.dbFileName)
			dataMapKeys, _, err := readRDB(filePath)
			if err != nil {
				fmt.Println("failed to load RDB file")
			}
			response := fmt.Sprintf("*%d", len(dataMapKeys))
			for key := range dataMapKeys {
				response += fmt.Sprintf("\r\n$%d\r\n%s", len(key), key)
			}
			return CommandResponse{Response: response}
		}
		return CommandResponse{Error: "-ERR unsupported KEYS pattern"}

	case RESP_COMMAND_INFO:
		var role string
		if s.state.serverIsMaster {
			role = "role:master"
		} else {
			role = "role:slave"
		}
		info := role + "\nmaster_replid:8371b4fb1155b71f4a04d3e1bc3e18c4a990aeeb\nmaster_repl_offset:0"
		resp := fmt.Sprintf("$%d\r\n%s", len(info), info)
		return CommandResponse{Response: resp}

	case RESP_COMMAND_REPLCONF:
		if !s.state.serverIsMaster {
			return CommandResponse{Error: "-ERR not allowed to slaves"}
		}
		return CommandResponse{Response: "+OK"}

	case RESP_COMMAND_PSYNC:
		if !s.state.serverIsMaster {
			return CommandResponse{Error: "-ERR not allowed to slaves"}
		}
		// This is a special case that needs direct connection handling
		s.conn.Write([]byte("+FULLRESYNC 8371b4fb1155b71f4a04d3e1bc3e18c4a990aeeb 0\r\n"))
		RDBContent, _ := hex.DecodeString("524544495330303131fa0972656469732d76657205372e322e30fa0a72656469732d62697473c040fa056374696d65c26d08bc65fa08757365642d6d656dc2b0c41000fa08616f662d62617365c000fff06e3bfec0ff5aa2")
		s.conn.Write([]byte(fmt.Sprintf("$%v\r\n%v", len(string(RDBContent)), string(RDBContent))))
		s.state.replicaConns = append(s.state.replicaConns, s.conn)
		return CommandResponse{}

	case RESP_COMMAND_TYPE:
		if len(tempArr) < 2 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'TYPE' command"}
		}
		s.state.storageMu.RLock()
		_, ok := s.state.storage[tempArr[1]]
		s.state.storageMu.RUnlock()
		if ok {
			return CommandResponse{Response: "+string"}
		} else {
			return CommandResponse{Response: "+none"}
		}

	case RESP_COMMAND_INCR:
		if len(tempArr) < 2 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'INCR' command"}
		}
		s.state.storageMu.Lock()
		val, ok := s.state.storage[tempArr[1]]

		if ok {
			value, err := strconv.Atoi(val.val.(string))
			if err != nil {
				s.state.storageMu.Unlock()
				return CommandResponse{Error: "-ERR value is not an integer or out of range"}
			} else {
				value++
				s.state.storage[tempArr[1]] = storageVal{val: strconv.Itoa(value), px: -1, t: time.Now()}
				resp := fmt.Sprintf(":%d", value)
				s.state.storageMu.Unlock()

				s.state.replicaMu.RLock()
				for _, replica := range s.state.replicaConns {
					replica.Write(encodeBulkArray(tempArr))
				}
				s.state.replicaMu.RUnlock()

				return CommandResponse{Response: resp}
			}
		} else {
			val := storageVal{val: "1", px: -1, t: time.Now()}
			s.state.storage[tempArr[1]] = val
			s.state.storageMu.Unlock()

			s.state.replicaMu.RLock()
			for _, replica := range s.state.replicaConns {
				replica.Write(encodeBulkArray(tempArr))
			}
			s.state.replicaMu.RUnlock()

			return CommandResponse{Response: ":1"}
		}

	case RESP_COMMAND_RPUSH:
		if len(tempArr) < 3 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'RPUSH' command"}
		}

		key := tempArr[1]
		newElems := tempArr[2:]

		s.state.storageMu.Lock()
		value, ok := s.state.storage[key]

		if ok {
			if list, ok := value.val.([]string); ok {
				list = append(list, newElems...)
				value.val = list
				s.state.storage[key] = value
			} else {
				s.state.storageMu.Unlock()
				return CommandResponse{Error: "-ERR wrong type of value for 'RPUSH' command"}
			}
		} else {
			s.state.storage[key] = storageVal{
				val: newElems,
				px:  -1,
				t:   time.Now(),
			}
		}
		listLen := len(s.state.storage[key].val.([]string))
		s.state.storageMu.Unlock()

		return CommandResponse{
			Response: ":" + strconv.Itoa(listLen) + "\r\n",
		}

	case RESP_COMMAND_LRANGE:
		if len(tempArr) < 4 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'LRANGE' command"}
		}

		key := tempArr[1]
		startIdx, err1 := strconv.Atoi(tempArr[2])
		endIdx, err2 := strconv.Atoi(tempArr[3])

		if err1 != nil || err2 != nil {
			return CommandResponse{Error: "-ERR value is not an integer or out of range"}
		}

		s.state.storageMu.RLock()
		value, ok := s.state.storage[key]
		s.state.storageMu.RUnlock()

		if !ok {
			return CommandResponse{Response: "*0\r\n"}
		}

		list, ok := value.val.([]string)
		if !ok {
			return CommandResponse{Error: "-ERR wrong type of value for 'LRANGE' command"}
		}

		listLen := len(list)

		if startIdx < 0 {
			startIdx = listLen + startIdx
			if startIdx < 0 {
				startIdx = 0
			}
		}
		if endIdx < 0 {
			endIdx = listLen + endIdx
			if endIdx < 0 {
				endIdx = 0
			}
		}

		if startIdx >= listLen || startIdx > endIdx {
			return CommandResponse{Response: "*0\r\n"}
		}
		if endIdx >= listLen {
			endIdx = listLen - 1
		}

		result := list[startIdx : endIdx+1]

		resp := "*" + strconv.Itoa(len(result)) + "\r\n"
		for _, elem := range result {
			resp += "$" + strconv.Itoa(len(elem)) + "\r\n" + elem + "\r\n"
		}

		return CommandResponse{Response: resp}

	case RESP_COMMAND_LPUSH:
		if len(tempArr) < 3 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'LPUSH' command"}
		}

		key := tempArr[1]
		newElems := tempArr[2:]

		s.state.storageMu.Lock()
		value, ok := s.state.storage[key]

		if ok {
			list, ok := value.val.([]string)
			if !ok {
				s.state.storageMu.Unlock()
				return CommandResponse{Error: "-ERR wrong type of value for 'LPUSH' command"}
			}
			for _, elem := range newElems {
				list = append([]string{elem}, list...)
			}
			value.val = list
			s.state.storage[key] = value
		} else {
			rev := make([]string, len(newElems))
			for i, v := range newElems {
				rev[len(newElems)-1-i] = v
			}
			s.state.storage[key] = storageVal{val: rev, px: -1, t: time.Now()}
		}
		listLen := len(s.state.storage[key].val.([]string))
		s.state.storageMu.Unlock()

		return CommandResponse{Response: ":" + strconv.Itoa(listLen) + "\r\n"}

	case RESP_COMMAND_LLEN:
		if len(tempArr) != 2 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'LLEN' command"}
		}

		key := tempArr[1]

		s.state.storageMu.RLock()
		value, ok := s.state.storage[key]
		s.state.storageMu.RUnlock()

		if !ok {
			return CommandResponse{Response: ":0\r\n"}
		}

		list, ok := value.val.([]string)
		if !ok {
			return CommandResponse{Error: "-ERR wrong type of value for 'LLEN' command"}
		}

		return CommandResponse{Response: ":" + strconv.Itoa(len(list)) + "\r\n"}

	case RESP_COMMAND_LPOP:
		if len(tempArr) < 2 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'LPOP' command"}
		}

		key := tempArr[1]

		s.state.storageMu.Lock()
		value, ok := s.state.storage[key]
		if !ok {
			s.state.storageMu.Unlock()
			return CommandResponse{Response: "$-1\r\n"}
		}

		list, ok := value.val.([]string)
		if !ok || len(list) == 0 {
			s.state.storageMu.Unlock()
			return CommandResponse{Response: "$-1\r\n"}
		}

		count := 1
		if len(tempArr) == 3 {
			var err error
			count, err = strconv.Atoi(tempArr[2])
			if err != nil || count < 0 {
				s.state.storageMu.Unlock()
				return CommandResponse{Error: "-ERR value is not an integer or out of range"}
			}
		}

		if count == 0 {
			s.state.storageMu.Unlock()
			return CommandResponse{Response: "*0\r\n"}
		}
		if count > len(list) {
			count = len(list)
		}

		removed := list[:count]
		remaining := list[count:]

		if len(remaining) == 0 {
			delete(s.state.storage, key)
		} else {
			value.val = remaining
			s.state.storage[key] = value
		}
		s.state.storageMu.Unlock()

		if count == 1 && len(tempArr) == 2 {
			elem := removed[0]
			return CommandResponse{Response: "$" + strconv.Itoa(len(elem)) + "\r\n" + elem + "\r\n"}
		}

		resp := "*" + strconv.Itoa(len(removed)) + "\r\n"
		for _, elem := range removed {
			resp += "$" + strconv.Itoa(len(elem)) + "\r\n" + elem + "\r\n"
		}
		return CommandResponse{Response: resp}

	case RESP_COMMAND_SUBSCRIBE:
		if len(tempArr) < 2 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'SUBSCRIBE' command"}
		}

		s.SubscribedMode = true

		responses := ""
		for _, chName := range tempArr[1:] {
			s.state.channelsMu.Lock()
			channel, exists := s.state.channels[chName]

			if !exists {
				channel = Channel{name: chName, connections: []*net.Conn{&s.conn}}
				s.state.channels[chName] = channel
			} else {
				channel.mu.Lock()
				already := false
				for _, c := range channel.connections {
					if *c == s.conn {
						already = true
						break
					}
				}
				if !already {
					channel.connections = append(channel.connections, &s.conn)
					s.state.channels[chName] = channel
				}
				channel.mu.Unlock()
			}
			s.state.channelsMu.Unlock()

			s.state.channelsMu.RLock()
			subCount := 0
			for _, ch := range s.state.channels {
				ch.mu.RLock()
				for _, c := range ch.connections {
					if *c == s.conn {
						subCount++
						break
					}
				}
				ch.mu.RUnlock()
			}
			s.state.channelsMu.RUnlock()

			resp := "*3\r\n" +
				"$9\r\nsubscribe\r\n" +
				"$" + strconv.Itoa(len(chName)) + "\r\n" + chName + "\r\n" +
				":" + strconv.Itoa(subCount) + "\r\n"

			responses += resp
		}

		return CommandResponse{Response: responses}

	case RESP_COMMAND_PUBLISH:
		if len(tempArr) < 3 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'PUBLISH' command"}
		}

		channelName := tempArr[1]
		message := tempArr[2]

		channel, exists := s.state.channels[channelName]
		if !exists {
			return CommandResponse{Response: ":0\r\n"}
		}

		deliver := "*3\r\n" +
			"$7\r\nmessage\r\n" +
			"$" + strconv.Itoa(len(channelName)) + "\r\n" + channelName + "\r\n" +
			"$" + strconv.Itoa(len(message)) + "\r\n" + message + "\r\n"

		for _, conn := range channel.connections {
			(*conn).Write([]byte(deliver))
		}

		count := len(channel.connections)
		return CommandResponse{Response: ":" + strconv.Itoa(count) + "\r\n"}

	case RESP_COMMAND_UNSUBSCRIBE:
		s.SubscribedMode = true

		responses := ""
		channelsToUnsub := tempArr[1:]
		if len(channelsToUnsub) == 0 {
			for chName, channel := range s.state.channels {
				newConns := []*net.Conn{}
				for _, c := range channel.connections {
					if *c != s.conn {
						newConns = append(newConns, c)
					}
				}
				channel.connections = newConns
				s.state.channels[chName] = channel

				subCount := 0
				for _, ch := range s.state.channels {
					for _, c := range ch.connections {
						if *c == s.conn {
							subCount++
							break
						}
					}
				}

				resp := "*3\r\n" +
					"$11\r\nunsubscribe\r\n" +
					"$" + strconv.Itoa(len(chName)) + "\r\n" + chName + "\r\n" +
					":" + strconv.Itoa(subCount) + "\r\n"

				responses += resp
			}
			return CommandResponse{Response: responses}
		}

		// Otherwise unsubscribe from the given channels
		for _, chName := range channelsToUnsub {
			channel, exists := s.state.channels[chName]
			if exists {
				newConns := []*net.Conn{}
				for _, c := range channel.connections {
					if *c != s.conn {
						newConns = append(newConns, c)
					}
				}
				channel.connections = newConns
				s.state.channels[chName] = channel
			}

			// Count how many channels remain for this client
			subCount := 0
			for _, ch := range s.state.channels {
				for _, c := range ch.connections {
					if *c == s.conn {
						subCount++
						break
					}
				}
			}

			resp := "*3\r\n" +
				"$11\r\nunsubscribe\r\n" +
				"$" + strconv.Itoa(len(chName)) + "\r\n" + chName + "\r\n" +
				":" + strconv.Itoa(subCount) + "\r\n"

			responses += resp
		}

		return CommandResponse{Response: responses}

	case RESP_COMMAND_ZADD:
		if len(tempArr) < 4 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'ZADD' command"}
		}

		key := tempArr[1]
		scoreStr := tempArr[2]
		member := tempArr[3]

		score, err := strconv.ParseFloat(scoreStr, 64)
		if err != nil {
			return CommandResponse{Error: "-ERR value is not a valid float"}
		}

		value, exists := s.state.storage[key]
		var zset *sortedset.SortedSet

		if exists {
			var ok bool
			zset, ok = value.val.(*sortedset.SortedSet)
			if !ok {
				return CommandResponse{Error: "-ERR wrong type of value for 'ZADD' command"}
			}
		} else {
			zset = sortedset.New()
			s.state.storage[key] = storageVal{val: zset, px: -1, t: time.Now()}
		}

		added := zset.AddOrUpdate(member, sortedset.SCORE(score), nil)

		if added {
			return CommandResponse{Response: ":1\r\n"}
		} else {
			return CommandResponse{Response: ":0\r\n"}
		}

	case RESP_COMMAND_ZRANK:
		if len(tempArr) != 3 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'ZRANK' command"}
		}

		key := tempArr[1]
		member := tempArr[2]

		value, exists := s.state.storage[key]
		if !exists {
			return CommandResponse{Response: "$-1"}
		}

		zset, ok := value.val.(*sortedset.SortedSet)
		if !ok {
			return CommandResponse{Error: "-ERR wrong type of value for 'ZRANK' command"}
		}

		r1 := zset.FindRank(member)
		if r1 == 0 {
			return CommandResponse{Response: "$-1"}
		}

		return CommandResponse{Response: fmt.Sprintf(":%d", r1-1)}

	case RESP_COMMAND_ZRANGE:
		if len(tempArr) < 4 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'ZRANGE' command"}
		}

		key := tempArr[1]
		startIdx, err1 := strconv.Atoi(tempArr[2])
		stopIdx, err2 := strconv.Atoi(tempArr[3])
		if err1 != nil || err2 != nil {
			return CommandResponse{Error: "-ERR value is not an integer or out of range"}
		}

		value, exists := s.state.storage[key]
		if !exists {
			return CommandResponse{Response: "*0\r\n"}
		}

		zset, ok := value.val.(*sortedset.SortedSet)
		if !ok {
			return CommandResponse{Error: "-ERR wrong type of value for 'ZRANGE' command"}
		}

		start, stop := convertIndexes(zset, startIdx, stopIdx)
		if start > stop || start > zset.GetCount() {
			return CommandResponse{Response: "*0\r\n"}
		}

		nodes := zset.GetByRankRange(start, stop, false)

		var b strings.Builder
		b.WriteString(fmt.Sprintf("*%d\r\n", len(nodes)))
		for _, node := range nodes {
			b.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(node.Key()), node.Key()))
		}

		return CommandResponse{Response: b.String()}

	case RESP_COMMAND_ZCARD:
		if len(tempArr) != 2 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'ZCARD' command"}
		}

		key := tempArr[1]
		value, exists := s.state.storage[key]
		if !exists {
			return CommandResponse{Response: ":0\r\n"}
		}

		zset, ok := value.val.(*sortedset.SortedSet)
		if !ok {
			return CommandResponse{Error: "-ERR wrong type of value for 'ZCARD' command"}
		}

		count := zset.GetCount()
		return CommandResponse{Response: fmt.Sprintf(":%d\r\n", count)}

	// case RESP_COMMAND_ZSCORE:
	// 	if len(tempArr) != 3 {
	// 		return CommandResponse{Error: "-ERR wrong number of arguments for 'ZSCORE' command"}
	// 	}

	// 	key := tempArr[1]
	// 	member := tempArr[2]

	// 	wz, ok := s.getZSet(key)
	// 	if !ok {
	// 		// key doesn't exist -> nil bulk string
	// 		return CommandResponse{Response: "$-1\r\n"}
	// 	}

	// 	// First try to return preserved original score string if present
	// 	if sstr, found := wz.scoreStr[member]; found {
	// 		return CommandResponse{Response: fmt.Sprintf("$%d\r\n%s\r\n", len(sstr), sstr)}
	// 	}

	// 	// Fallback: check the node in sortedset (if wrapper doesn't have score string)
	// 	node := wz.set.GetByKey(member)
	// 	if node == nil {
	// 		return CommandResponse{Response: "$-1\r\n"}
	// 	}

	// 	// If we fall here, format the numeric score (best-effort)
	// 	scoreStrFormatted := strconv.FormatFloat(float64(node.Score()), 'f', -1, 64)
	// 	return CommandResponse{Response: fmt.Sprintf("$%d\r\n%s\r\n", len(scoreStrFormatted), scoreStrFormatted)}

	case RESP_COMMAND_ZREM:
		if len(tempArr) != 3 {
			return CommandResponse{Error: "-ERR wrong number of arguments for 'ZREM' command"}
		}

		key := tempArr[1]
		member := tempArr[2]

		value, exists := s.state.storage[key]
		if !exists {
			return CommandResponse{Response: ":0\r\n"}
		}

		zset, ok := value.val.(*sortedset.SortedSet)
		if !ok {
			return CommandResponse{Error: "-ERR wrong type of value for 'ZREM' command"}
		}

		removed := zset.Remove(member)
		if removed == nil {
			return CommandResponse{Response: ":0\r\n"}
		}
		return CommandResponse{Response: ":1\r\n"}

	default:
		return CommandResponse{Error: "-ERR unknown command"}
	}
}
