package k8s

import (
	"context"

	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/cluster-management/pkg/utils"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	cli "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultScheme = "https://"
)

var (
	minNumber = 5

	scheme    = runtime.NewScheme()
	clioption = cli.Options{Scheme: scheme}

	defaultTransport = &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
}

//param: apiserver host ip
//return: token,error
type AuthProvider func(string) (string, error)

// regist new apiserver address
// pull status duration
type Manage struct {
	ctx  context.Context
	pool *utils.Pool

	du time.Duration

	mu sync.RWMutex

	//key: host address
	cache map[string]*Client

	authfh AuthProvider
}

func NewManage(du time.Duration, workerNum int, fn AuthProvider) *Manage {
	if workerNum < minNumber {
		workerNum = minNumber
	}

	if fn == nil {
		panic("auth function should not be nil")
	}
	return &Manage{
		ctx:    context.Background(),
		pool:   utils.NewPool(workerNum),
		du:     du,
		mu:     sync.RWMutex{},
		cache:  make(map[string]*Client),
		authfh: fn,
	}
}

func (s *Manage) LoopRun(ctx context.Context) {
	var (
		wg  sync.WaitGroup
		err error
	)

	for {
		select {
		case <-ctx.Done():
			klog.Errorf("ctx failed:%v", ctx.Err())
			return
		case <-time.NewTimer(s.du).C:
			klog.V(3).Infof("start update k8s nodes")
			s.mu.RLock()
			for k, v := range s.cache {
				var (
					tmpk   = k
					tmpcli = v
				)
				if tmpcli == nil {
					klog.V(2).Infof("not found client on %s", tmpk)
					continue
				}
				wg.Add(1)
				err = s.pool.Submit(func() {
					defer wg.Done()
					uerr := tmpcli.update()
					if uerr != nil {
						klog.Errorf("update host %v failed:%v", tmpk, uerr)
					}
				})
				if err != nil {
					klog.Errorf("submit k8s task fail:%v", err)
					wg.Done()
				}
			}
			s.mu.RUnlock()
			wg.Wait()
			klog.V(3).Infof("end update k8s nodes")
		}
	}
}

func (s *Manage) Del(host string) {
	newh, err := parseHost(host)
	if err != nil {
		klog.Errorf("parse host failed:%v", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	klog.Infof("delete cache on host %v", newh)
	delete(s.cache, newh)
}

//get and update cache
func (s *Manage) Get(host string) (*Client, error) {
	newh, err := parseHost(host)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.cache[newh]; !ok {
		s.cache[newh] = newClient(func() (client cli.Client, err error) {
			return s.newClient(host)
		}, host)
	}
	return s.cache[newh], nil
}

func (s *Manage) newClient(host string) (cli.Client, error) {
	//TODO(yylt) token can use same one
	token, err := s.authfh(host)
	if err != nil {
		return nil, err
	}
	config := &rest.Config{
		Host:        host,
		BearerToken: token,
		Transport:   defaultTransport,
		Burst:       3,
		Timeout:     30 * time.Second,
	}
	return cli.New(config, clioption)
}

func parseHost(host string) (string, error) {
	u, err := url.Parse(host)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" {
		u.Scheme = DefaultScheme
	}
	return u.String(), nil
}
