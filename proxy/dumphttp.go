package proxy

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/fluent/fluent-logger-golang/fluent"
	"github.com/golang/glog"
	gouuid "github.com/satori/go.uuid"
)

func dumphttpRequest(id []byte, f *fluent.Fluent, msg []byte) {
	if Conf.DumpInWireFormat {
		e := f.EncodeAndPostData(fmt.Sprintf("%s.%s", f.Config.TagPrefix, "request"), time.Now(), msg)
		if e != nil {
			glog.Errorf("proxy: dumphttprequest: unable to post to fluentd: %q", e)
			return
		}
		return
	}
	uuid, err := gouuid.FromBytes(id)
	if err != nil {
		glog.Error("proxy: dumphttprequest: unable to parse uuid")
		return
	}
	b := bufio.NewReader(bytes.NewReader(msg))
	w := new(bytes.Buffer)
	rq, e := http.ReadRequest(b)
	if e != nil {
		glog.Errorf("proxy: dumphttprequest: unable to readrequest: %q", e)
		return
	}
	rq.Header.Write(w)
	header := w.Bytes()
	body, e := ioutil.ReadAll(rq.Body)
	if e != nil {
		glog.Errorf("proxy: dumphttprequest: unable to read body: %q", e)
		return
	}
	req := map[string]string{
		"uuid":   uuid.String(),
		"host":   rq.Host,
		"method": rq.Method,
		"url":    rq.URL.String(),
		"header": string(header[:]),
		"body":   string(body[:]),
	}

	e = f.Post("request", req)
	if e != nil {
		glog.Errorf("proxy: dumphttprequest: unable to post to fluentd: %q", e)
		return
	}
}

func dumphttpResponse(id []byte, f *fluent.Fluent, msg []byte) {
	if Conf.DumpInWireFormat {
		e := f.EncodeAndPostData(fmt.Sprintf("%s.%s", f.Config.TagPrefix, "response"), time.Now(), msg)
		if e != nil {
			glog.Errorf("proxy: dumphttpresponse: unable to post to fluentd: %q", e)
			return
		}
		return
	}
	uuid, err := gouuid.FromBytes(id)
	if err != nil {
		glog.Error("proxy: dumphttpresponse: unable to parse uuid")
		return
	}
	b := bufio.NewReader(bytes.NewReader(msg))
	w := new(bytes.Buffer)
	rsp, e := http.ReadResponse(b, nil)
	if e != nil {
		glog.Errorf("proxy: dumphttpresponse: unable to readresponse: %q", e)
		return
	}
	rsp.Header.Write(w)
	header := w.Bytes()
	body, e := ioutil.ReadAll(rsp.Body)
	if e != nil {
		glog.Errorf("proxy: dumphttpresponse: unable to read body: %q", e)
		return
	}
	resp := map[string]string{
		"uuid":   uuid.String(),
		"status": fmt.Sprintf("%d", rsp.StatusCode),
		"header": string(header[:]),
		"body":   string(body[:]),
	}
	e = f.Post("response", resp)
	if e != nil {
		glog.Errorf("proxy: dumphttpresponse: unable to post to fluentd: %q", e)
		return
	}
}
