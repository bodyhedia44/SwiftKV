package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
)

func main() {
	fmt.Println("Logs...")

	dir := flag.String("dir", "", "Directory to store the database")
	dbfilename := flag.String("dbfilename", "", "Database file name")
	port_arg := flag.String("port", "", "Database file name")
	replicaOf := flag.String("replicaof", "", "The host and port of master server")

	flag.Parse()

	var port string

	if *port_arg == "" {
		port = "6379"
	} else {
		port = *port_arg
	}

	sharedState := &RedisState{
		storage: make(map[string]storageVal),
		config: Config{
			Directory:  *dir,
			dbFileName: *dbfilename,
		},
		replicaConns: []net.Conn{},
		channels:     make(map[string]Channel),
	}

	if *replicaOf == "" {
		sharedState.serverIsMaster = true
	} else {
		sharedState.serverIsMaster = false
		masterHost, masterPort, err := extractReplicaInfo(replicaOf)
		if err != nil {
			log.Fatalf("wrong replicaof argument format: %v\n", err)
		}
		conn, err := net.Dial("tcp", masterHost+":"+masterPort)
		if err != nil {
			fmt.Println("Failed to connect to master: ", err.Error())
			os.Exit(1)
		}
		initHandShake(conn, port, sharedState)
	}

	l, err := net.Listen("tcp", "0.0.0.0:"+port)
	if err != nil {
		fmt.Println("Failed to bind to port " + port)
		os.Exit(1)
	}

	for {
		conn, err := l.Accept()

		if err != nil {
			fmt.Println("Error accepting connection: ", err.Error())
			os.Exit(1)
		}
		redisServer := RedisServer{
			state:      sharedState,
			conn:       conn,
			MultiOn:    false,
			multiQueue: [][]string{},
		}
		go redisServer.handleConnection()
	}
}
