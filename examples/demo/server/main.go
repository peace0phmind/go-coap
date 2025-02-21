package main

import (
	"bytes"
	"fmt"
	"log"
	gonet "net"
	"time"

	coap "github.com/plgd-dev/go-coap/v3"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/mux"
	"github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/udp"
)

func getPath(opts message.Options) string {
	path, err := opts.Path()
	if err != nil {
		log.Printf("cannot get path: %v", err)
		return ""
	}
	return path
}

func sendResponse(cc mux.Conn, token []byte, subded time.Time, obs int64) error {
	m := cc.AcquireMessage(cc.Context())
	defer cc.ReleaseMessage(m)
	m.SetCode(codes.Content)
	m.SetToken(token)
	m.SetBody(bytes.NewReader([]byte(fmt.Sprintf("Been running for %v", time.Since(subded)))))
	m.SetContentFormat(message.TextPlain)
	if obs >= 0 {
		m.SetObserve(uint32(obs))
	}
	return cc.WriteMessage(m)
}

func periodicTransmitter(cc mux.Conn, token []byte) {
	subded := time.Now()

	for obs := int64(2); ; obs++ {
		err := sendResponse(cc, token, subded, obs)
		if err != nil {
			log.Printf("Error on transmitter, stopping: %v", err)
			return
		}
		time.Sleep(time.Second)
	}
}

func handleMcast(w mux.ResponseWriter, r *mux.Message) {
	path, err := r.Options().Path()
	if err != nil {
		log.Printf("cannot get path: %v", err)
		return
	}

	log.Printf("Got mcast message: path=%q: from %v", path, w.Conn().RemoteAddr())
	w.SetResponse(codes.Content, message.TextPlain, bytes.NewReader([]byte("mcast response")))
}

func handleObserve(w mux.ResponseWriter, r *mux.Message) {
	log.Printf("Got message path=%v: %+v from %v", getPath(r.Options()), r, w.Conn().RemoteAddr())
	obs, err := r.Options().Observe()
	switch {
	case r.Code() == codes.GET && err == nil && obs == 0:
		go periodicTransmitter(w.Conn(), r.Token())
	case r.Code() == codes.GET:
		err := sendResponse(w.Conn(), r.Token(), time.Now(), -1)
		if err != nil {
			log.Printf("Error on transmitter: %v", err)
		}
	}
}

func main() {
	// 创建路由器
	m := mux.NewRouter()

	// 注册处理函数
	m.Handle("/oic/res", mux.HandlerFunc(handleMcast))
	m.Handle("/observe", mux.HandlerFunc(handleObserve))

	// 设置多播地址和端口
	multicastAddr := "224.0.1.187:5683"

	// 创建UDP监听器
	l, err := net.NewListenUDP("udp4", multicastAddr)
	if err != nil {
		log.Fatal(err)
	}

	// 获取网络接口
	ifaces, err := gonet.Interfaces()
	if err != nil {
		log.Fatal(err)
	}

	// 解析多播地址
	a, err := gonet.ResolveUDPAddr("udp", multicastAddr)
	if err != nil {
		log.Fatal(err)
	}

	// 加入多播组
	for i := range ifaces {
		iface := ifaces[i]
		err := l.JoinGroup(&iface, a)
		if err != nil {
			log.Printf("cannot JoinGroup(%v, %v): %v", iface, a, err)
		}
	}

	// 设置多播回环
	err = l.SetMulticastLoopback(true)
	if err != nil {
		log.Fatal(err)
	}

	defer l.Close()

	// 创建UDP服务器
	s := udp.NewServer(options.WithMux(m))
	defer s.Stop()

	// 启动普通的CoAP服务器（用于Observer）
	go func() {
		log.Printf("Starting CoAP server on :5688")
		log.Fatal(coap.ListenAndServe("udp4", ":5688", m))
	}()

	// 启动多播服务器
	log.Printf("Starting multicast server on %v", multicastAddr)
	log.Fatal(s.Serve(l))
}
