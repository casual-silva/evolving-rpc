package evolving_server

import (
	"encoding/json"
	"evolving-rpc/contents"
	"evolving-rpc/errorx"
	"evolving-rpc/evolving-server/svr_mgr"
	"evolving-rpc/model"
	"fmt"
	"gitee.com/yuhao-jack/go-toolx/netx"
	"log"
	"net"
	"sync"
)

var logger = log.Default()

func init() {
	logger.SetFlags(log.Llongfile | log.Ldate | log.Lmicroseconds)
}

// EvolvingServer
// @Description:
type EvolvingServer struct {
	conf            *model.EvolvingServerConf
	dataPackChanMap map[*netx.DataPack]chan netx.IMessage
	commands        map[string]func(dataPack *netx.DataPack, reply netx.IMessage)
	lock            sync.RWMutex
}

// NewEvolvingServer
//
//	@Description:
//	@param conf
//	@return *EvolvingServer
func NewEvolvingServer(conf *model.EvolvingServerConf) *EvolvingServer {
	evolvingServer := EvolvingServer{conf: conf, dataPackChanMap: make(map[*netx.DataPack]chan netx.IMessage)}
	evolvingServer.SetCommand(contents.ALive, func(dataPack *netx.DataPack, reply netx.IMessage) {
		evolvingServer.sendMsg(dataPack, netx.NewDefaultMessage([]byte(contents.ALive), []byte(contents.OK)))
	})
	evolvingServer.SetCommand(contents.Default, func(dataPack *netx.DataPack, reply netx.IMessage) {
		evolvingServer.sendMsg(dataPack, netx.NewDefaultMessage([]byte(contents.Default), []byte(contents.OK)))
	})
	return &evolvingServer
}

// Start
//
//	@Description:
//	@receiver s
//	@param conf
func (s *EvolvingServer) Start() {
	tcpListener, err := netx.CreateTCPListener(fmt.Sprintf("%s:%d", s.conf.BindHost, s.conf.ServerPort))
	if err != nil {
		logger.Println("start evolving-server failed,err:", err)
		return
	}
	logger.Println("start evolving-server successful.")
	for {
		tcpConn, err := tcpListener.AcceptTCP()
		if err != nil {
			logger.Println("accept tcp conn failed,err:", err)
			continue
		}
		go s.connHandler(tcpConn)
	}
}

// connHandler
//
//	@Description:
//	@param conn
func (s *EvolvingServer) connHandler(conn *net.TCPConn) {
	dataPack := netx.DataPack{Conn: conn}
	svr_mgr.GetServiceMgrInstance().AddDataPack(&dataPack)

	defer func() {
		svr_mgr.GetServiceMgrInstance().DelDataPack(&dataPack)
		err := conn.Close()
		if err != nil {
			logger.Println(err)
		}
		close(s.dataPackChanMap[&dataPack])
		delete(s.dataPackChanMap, &dataPack)
		s.broadCast(netx.NewDefaultMessage([]byte("connect_closed"), []byte(dataPack.RemoteAddr().String()+" closed")))
	}()
	for {
		message, err := dataPack.UnPackMessage()
		if err != nil {
			logger.Println(err)
			break
		}
		if f, ok := s.commands[string(message.GetCommand())]; ok {
			f(&dataPack, message)
		} else {
			s.commands[contents.Default](&dataPack, message)
		}
	}
}

// Execute
//
//	@Description:
//	@receiver s
//	@param dataPack
//	@param req
//	@param callBack
func (s *EvolvingServer) Execute(dataPack *netx.DataPack, req netx.IMessage, callBack func(dataPack *netx.DataPack, reply netx.IMessage)) {
	s.SetCommand(string(req.GetCommand()), callBack)
	s.sendMsg(dataPack, req)
}

// SetCommand
//
//	@Description:
//	@receiver s
//	@param command
//	@param f
func (s *EvolvingServer) SetCommand(command string, f func(dataPack *netx.DataPack, reply netx.IMessage)) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.commands[command] = f
}

// GetCommand
//
//	@Description:
//	@receiver s
//	@param command
//	@return f
func (s *EvolvingServer) GetCommand(command string) (f func(dataPack *netx.DataPack, reply netx.IMessage)) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	f = s.commands[command]
	return
}

// broadCast
//
//	@Description:
//	@param msg
func (s *EvolvingServer) broadCast(msg netx.IMessage) {
	for _, pack := range svr_mgr.GetServiceMgrInstance().DataPackMap {
		s.sendMsg(pack, msg)
	}
}

// sendMsg
//
//	@Description:
//	@param dataPack
//	@param message
func (s *EvolvingServer) sendMsg(dataPack *netx.DataPack, message netx.IMessage) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	_, ok := s.dataPackChanMap[dataPack]
	if !ok {
		s.lock.Lock()
		s.dataPackChanMap[dataPack] = make(chan netx.IMessage, 1024)
		s.lock.Unlock()
		go func() {
			for {
				select {
				case msg, ok := <-s.dataPackChanMap[dataPack]:
					if ok {
						err := dataPack.PackMessage(msg)
						if err != nil {
							logger.Println(err)
						}
					} else {
						logger.Println(dataPack.RemoteAddr().String(), " closed")
						break
					}
				}
			}
		}()
	}
	s.dataPackChanMap[dataPack] <- message
}

// KeepAlive
//
//	@Description:
//	@param message
//	@param dataPack
func KeepAlive(message netx.IMessage, dataPack *netx.DataPack, sendMsg func(dataPack *netx.DataPack, message netx.IMessage)) {
	sendMsg(dataPack, message)
}

// Register
//
//	@Description:
//	@param message
//	@param dataPack
func Register(message netx.IMessage, dataPack *netx.DataPack, sendMsg func(dataPack *netx.DataPack, message netx.IMessage)) {
	var serviceInfo model.ServiceInfo
	err := json.Unmarshal(message.GetBody(), &serviceInfo)
	if err != nil {
		logger.Println(err)
		logger.Println(string(message.GetBody()))
		return
	}
	needInsert := true
	for _, info := range svr_mgr.GetServiceMgrInstance().ServiceInfoList {
		if info.ServiceName == serviceInfo.ServiceName &&
			info.ServiceHost == serviceInfo.ServiceHost &&
			info.ServicePort == serviceInfo.ServicePort {
			info.AdditionalMeta = serviceInfo.AdditionalMeta
			info.ServiceProtoc = serviceInfo.ServiceProtoc
			needInsert = false
		}
	}
	if needInsert {
		svr_mgr.GetServiceMgrInstance().AddServiceInfo(&serviceInfo)
	}
	KeepAlive(message, dataPack, sendMsg)
}

// DisCover
//
//	@Description:
//	@param message
//	@param dataPack
func DisCover(message netx.IMessage, dataPack *netx.DataPack, sendMsg func(dataPack *netx.DataPack, message netx.IMessage)) {
	list := svr_mgr.GetServiceMgrInstance().FindServiceInfosByServiceName(string(message.GetBody()))
	bytes, err := json.Marshal(list)
	if err != nil {
		logger.Println(err)
		return
	}
	message.SetBody(bytes)
	sendMsg(dataPack, message)
}

// Default
//
//	@Description:
//	@param message
//	@param dataPack
func Default(message netx.IMessage, dataPack *netx.DataPack, sendMsg func(dataPack *netx.DataPack, message netx.IMessage)) {
	message.SetBody([]byte(errorx.UnknownCommandErr.Error()))
	sendMsg(dataPack, message)
}
