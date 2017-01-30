package xbmc

import (
	"net"

	"github.com/scakemyer/quasar/jsonrpc"
)

type Args []interface{}
type Object map[string]interface{}

var Results map[string]chan interface{}

var (
	XBMCJSONRPCHosts = []string{
		net.JoinHostPort("127.0.0.1", "9090"),
	}
	XBMCExJSONRPCHosts = []string{
		net.JoinHostPort("127.0.0.1", "65252"),
	}
)

func getConnection(hosts ...string) (net.Conn, error) {
	var err error

	for _, host := range hosts {
		c, err := net.Dial("tcp", host)
		if err == nil {
			return c, nil
		}
	}

	return nil, err
}

func executeJSONRPC(method string, retVal interface{}, args Args) error {
	if args == nil {
		args = Args{}
	}
	conn, err := getConnection(XBMCJSONRPCHosts...)
	if err != nil {
		log.Error(err)
		log.Critical("No available JSON-RPC connection to Kodi")
		return err
	}
	defer conn.Close()

	client := jsonrpc.NewClient(conn)
	return client.Call(method, args, retVal)
}

func executeJSONRPCO(method string, retVal interface{}, args Object) error {
	if args == nil {
		args = Object{}
	}
	conn, err := getConnection(XBMCJSONRPCHosts...)
	if err != nil {
		log.Error(err)
		log.Critical("No available JSON-RPC connection to Kodi")
		return err
	}
	defer conn.Close()

	client := jsonrpc.NewClient(conn)
	return client.Call(method, args, retVal)
}

func executeJSONRPCEx(method string, retVal interface{}, args Args) error {
	if args == nil {
		args = Args{}
	}
	conn, err := getConnection(XBMCExJSONRPCHosts...)
	if err != nil {
		log.Error(err)
		log.Critical("No available JSON-RPC connection to the add-on.")
		return err
	}
	defer conn.Close()

	client := jsonrpc.NewClient(conn)
	return client.Call(method, args, retVal)
}
