package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wangjia184/sortedset"
)

func extractReplicaInfo(replicaOf *string) (string, string, error) {
	if *replicaOf == "" {
		return "", "", nil
	}

	replicaOfArgs := strings.Split(*replicaOf, " ")
	if len(replicaOfArgs) != 2 {
		return "", "", fmt.Errorf("provide host and port separated by space, e.g: '0.0.0.0 6379'")
	}

	masterHost := replicaOfArgs[0]
	masterPort := replicaOfArgs[1]
	if masterHost == "" || masterPort == "" {
		return "", "", fmt.Errorf("host and port must be non-empty")
	}

	// atoiMasterPort, err := strconv.Atoi(masterPort)
	// if err != nil {
	// 	return "","", fmt.Errorf("port should be decimal: %v", err)
	// }

	return masterHost, masterPort, nil
}

func readRDB(filePath string) (map[string]string, map[string]time.Time, error) {
	dataMap := make(map[string]string)
	dataMapTime := make(map[string]time.Time)

	data, err := os.ReadFile(filePath)
	fmt.Println(data)
	fmt.Println(string(data))

	if err != nil {
		return dataMap, dataMapTime, nil
	}
	if len(data) < 9 {
		return dataMap, dataMapTime, nil
	}

	if string(data[:9]) != "REDIS0011" {
		return dataMap, dataMapTime, nil
	}

	index := 9

	for index < len(data) && data[index] != 0xFB {
		index++
	}
	fmt.Printf("we here → index=%d, byte=0x%X\n", index, data[index]) // at 0xFB
	index += 4

	key, err := readString(data, &index)
	if err != nil {
		return dataMap, dataMapTime, nil
	}
	value, err := readString(data, &index)
	if err != nil {
		return dataMap, dataMapTime, nil
	}
	dataMap[key] = value
	fmt.Print("Loaded key: '%s', value: '%s'\n", key, value)
	return dataMap, dataMapTime, nil

}

func readString(data []byte, index *int) (string, error) {
	firstByte := data[*index]
	*index += 1

	prefix := firstByte & 0xC0 //mask the first two bits
	var length int
	var str string

	switch prefix {

	case 0x00: //6 bit
		length = int(firstByte & 0x3F)

	case 0x40: //14 bit
		if *index >= len(data) {
			return "", fmt.Errorf("Error end of data(14 bit)")
		}
		secondByte := data[*index]
		*index += 1
		length = int(firstByte&0x3f)<<8 | int(secondByte)

	case 0x80: // 32 bit
		if *index+4 > len(data) {
			return "", fmt.Errorf("not enough bytes")
		}
		length = int(binary.BigEndian.Uint32(data[*index : *index+4]))
		*index += 4

	case 0xC0: //special case
		return "", fmt.Errorf("unsupported string encoding")
	}

	if *index+length > len(data) {
		return "", fmt.Errorf("not enough data to read string of %d", length)
	}
	str = string(data[*index : *index+length])
	*index += length
	return str, nil
}

func RESPToArray(resp string) ([]string, error) {
	if !strings.HasPrefix(resp, "*") {
		return nil, errors.New("invalid RESP: missing '*' for array")
	}
	i := strings.Index(resp, "\r\n")
	if i < 0 {
		return nil, errors.New("invalid RESP: missing CRLF after array length")
	}

	count, err := strconv.Atoi(resp[1:i])
	if err != nil {
		return nil, fmt.Errorf("invalid array length: %v", err)
	}

	elements := make([]string, 0, count)
	rest := resp[i+2:]

	for idx := 0; idx < count; idx++ {
		if !strings.HasPrefix(rest, "$") {
			return nil, fmt.Errorf("invalid RESP: element %d missing '$'", idx)
		}
		j := strings.Index(rest, "\r\n")
		if j < 0 {
			return nil, fmt.Errorf("invalid RESP: missing CRLF after bulk length at element %d", idx)
		}

		length, err := strconv.Atoi(rest[1:j])
		if err != nil {
			return nil, fmt.Errorf("invalid bulk length at element %d: %v", idx, err)
		}

		dataStart := j + 2
		dataEnd := dataStart + length
		if len(rest) < dataEnd+2 {
			return nil, fmt.Errorf("invalid RESP: incomplete data for element %d", idx)
		}

		elem := rest[dataStart:dataEnd]
		if rest[dataEnd:dataEnd+2] != "\r\n" {
			return nil, fmt.Errorf("invalid RESP: missing terminating CRLF for element %d", idx)
		}

		elements = append(elements, elem)
		rest = rest[dataEnd+2:]
	}

	return elements, nil
}
func WriteBulkString(conn net.Conn, s string) {
	response := fmt.Sprintf("$%d\r\n%s\r\n", len(s), s)
	_, err := conn.Write([]byte(response))
	if err != nil {
		fmt.Println("Failed to write bulk string '%s': %v", s, err)
	} else {
		fmt.Println("Wrote bulk string (%d bytes): %s", len(s), s)
	}
}

func WriteSimpleString(conn net.Conn, s string) {
	response := fmt.Sprintf("+%s\r\n", s)
	_, err := conn.Write([]byte(response))
	if err != nil {
		fmt.Println("Failed to write simple string '%s': %v", s, err)
	} else {
		fmt.Println("Wrote simple string: +%s", s)
	}
}

func encodeBulkArray(output []string) []byte {
	result := fmt.Sprintf("*%d\r\n", len(output))

	for _, o := range output {
		result += fmt.Sprintf("$%d\r\n%s\r\n", len(o), o)
	}

	return []byte(result)
}

func parseRESPCommand(data string) ([]string, string, error) {
	if len(data) == 0 {
		return nil, data, fmt.Errorf("no data")
	}
	if data[0] != '*' {
		return nil, data, fmt.Errorf("not a RESP array")
	}
	firstCRLF := strings.Index(data, "\r\n")
	if firstCRLF == -1 {
		return nil, data, fmt.Errorf("incomplete array size")
	}

	arraySizeStr := data[1:firstCRLF]
	arraySize, err := strconv.Atoi(arraySizeStr)
	if err != nil {
		return nil, data, err
	}

	result := make([]string, arraySize)
	pos := firstCRLF + 2

	for i := 0; i < arraySize; i++ {
		if pos >= len(data) || data[pos] != '$' {
			return nil, data, fmt.Errorf("incomplete data")
		}

		lengthEnd := strings.Index(data[pos:], "\r\n")
		if lengthEnd == -1 {
			return nil, data, fmt.Errorf("incomplete string length")
		}
		lengthEnd += pos

		lengthStr := data[pos+1 : lengthEnd]
		stringLength, err := strconv.Atoi(lengthStr)
		if err != nil {
			return nil, data, err
		}

		pos = lengthEnd + 2

		if pos+stringLength+2 > len(data) {
			return nil, data, fmt.Errorf("incomplete string data")
		}

		result[i] = data[pos : pos+stringLength]
		pos += stringLength + 2
	}

	return result, data[pos:], nil
}

func RESPToArrayWithOffset(input string) ([]string, int, error) {
	if len(input) == 0 || input[0] != '*' {
		return nil, 0, fmt.Errorf("expected RESP array")
	}

	// Parse array length
	newline := strings.Index(input, "\r\n")
	if newline == -1 {
		return nil, 0, fmt.Errorf("incomplete array header")
	}
	numElems, err := strconv.Atoi(input[1:newline])
	if err != nil {
		return nil, 0, err
	}

	elems := []string{}
	idx := newline + 2
	for i := 0; i < numElems; i++ {
		if idx >= len(input) || input[idx] != '$' {
			return nil, 0, fmt.Errorf("expected bulk string")
		}

		nl := strings.Index(input[idx:], "\r\n")
		if nl == -1 {
			return nil, 0, fmt.Errorf("incomplete bulk length")
		}
		bulkLen, err := strconv.Atoi(input[idx+1 : idx+nl])
		if err != nil {
			return nil, 0, err
		}

		start := idx + nl + 2
		end := start + bulkLen
		if end+2 > len(input) {
			return nil, 0, fmt.Errorf("incomplete bulk data")
		}

		elems = append(elems, input[start:end])
		idx = end + 2
	}

	return elems, idx, nil // idx = total bytes consumed
}

func toRespStr(raw string) string {
	length := len(raw)
	return fmt.Sprintf("$%d\r\n%s\r\n", length, raw)
}

func toRespArr(strs ...string) string {
	respArr := fmt.Sprintf("*%d\r\n", len(strs))
	for _, str := range strs {
		respArr += toRespStr(str)
	}

	return respArr
}

func convertIndexes(zset *sortedset.SortedSet, startIdx, stopIdx int) (int, int) {
	length := zset.GetCount()

	if startIdx < 0 {
		startIdx = length + startIdx
	}
	if stopIdx < 0 {
		stopIdx = length + stopIdx
	}

	if startIdx < 0 {
		startIdx = 0
	}
	if stopIdx < 0 {
		stopIdx = 0
	}
	if startIdx >= length {
		startIdx = length
	}
	if stopIdx >= length {
		stopIdx = length - 1
	}

	start := startIdx + 1
	stop := stopIdx + 1

	return start, stop
}
