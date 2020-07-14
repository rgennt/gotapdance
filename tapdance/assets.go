package tapdance

import (
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io/ioutil"
	"net"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/golang/protobuf/proto"
	pb "github.com/refraction-networking/gotapdance/protobuf"
)

type assets struct {
	sync.RWMutex
	path string

	config pb.ClientConf

	roots *x509.CertPool

	filenameRoots      string
	filenameClientConf string

	socksAddr string
}

// could reset this internally to refresh assets and avoid woes of singleton testing
var assetsInstance *assets
var assetsOnce sync.Once

// Assets is an access point to asset managing singleton.
// First access to singleton sets path. Assets(), if called
// before SetAssetsDir() sets path to "./assets/"
func Assets() *assets {
	_initAssets := func() { initAssets("./assets/") }
	assetsOnce.Do(_initAssets)
	return assetsInstance
}

// AssetsSetDir sets the directory to read assets from.
// Functionally equivalent to Assets() after initialization, unless dir changes.
func AssetsSetDir(dir string) *assets {
	_initAssets := func() { initAssets(dir) }
	if assetsInstance != nil {
		assetsInstance.Lock()
		defer assetsInstance.Unlock()
		if dir != assetsInstance.path {
			Logger().Warnf("Assets path changed %s->%s. (Re)initializing.\n",
				assetsInstance.path, dir)
			assetsInstance.path = dir
			assetsInstance.readConfigs()
			return assetsInstance
		}
	}
	assetsOnce.Do(_initAssets)
	return assetsInstance
}

func getDefaultKey() []byte {
	// keyStr := "515868be7f45ab6f310afed4b229b7a479fc9fde553dea4ccdb369ab1899e70c"
	keyStr := "a1cb97be697c5ed5aefd78ffa4db7e68101024603511e40a89951bc158807177"
	key := make([]byte, hex.DecodedLen(len(keyStr)))
	hex.Decode(key, []byte(keyStr))
	return key
}

func initAssets(path string) {
	var defaultDecoys = []*pb.TLSDecoySpec{
		pb.InitTLSDecoySpec("192.122.190.104", "tapdance1.freeaeskey.xyz"),
		pb.InitTLSDecoySpec("192.122.190.105", "tapdance2.freeaeskey.xyz"),
		pb.InitTLSDecoySpec("192.122.190.106", "tapdance3.freeaeskey.xyz"),
	}

	defaultKey := getDefaultKey()
	defualtKeyType := pb.KeyType_AES_GCM_128
	defaultPubKey := pb.PubKey{Key: defaultKey, Type: &defualtKeyType}
	defaultGeneration := uint32(0)
	defaultDecoyList := pb.DecoyList{TlsDecoys: defaultDecoys}
	defaultClientConf := pb.ClientConf{DecoyList: &defaultDecoyList,
		DefaultPubkey: &defaultPubKey,
		Generation:    &defaultGeneration}

	assetsInstance = &assets{
		path:               path,
		config:             defaultClientConf,
		filenameRoots:      "roots",
		filenameClientConf: "ClientConf",
		socksAddr:          "",
	}
	assetsInstance.readConfigs()
}

func (a *assets) GetAssetsDir() string {
	a.RLock()
	defer a.RUnlock()
	return a.path
}

func (a *assets) readConfigs() {
	readRoots := func(filename string) error {
		rootCerts, err := ioutil.ReadFile(filename)
		if err != nil {
			return err
		}
		roots := x509.NewCertPool()
		ok := roots.AppendCertsFromPEM(rootCerts)
		if !ok {
			return errors.New("Failed to parse root certificates")
		}
		a.roots = roots
		return nil
	}

	readClientConf := func(filename string) error {
		buf, err := ioutil.ReadFile(filename)
		if err != nil {
			return err
		}
		clientConf := pb.ClientConf{}
		err = proto.Unmarshal(buf, &clientConf)
		if err != nil {
			return err
		}
		a.config = clientConf
		return nil
	}

	var err error
	Logger().Infoln("Assets: reading from folder " + a.path)

	rootsFilename := path.Join(a.path, a.filenameRoots)
	err = readRoots(rootsFilename)
	if err != nil {
		Logger().Warningln("Assets: failed to read root ca file: " + err.Error())
	} else {
		Logger().Infoln("X.509 root CAs successfully read from " + rootsFilename)
	}

	clientConfFilename := path.Join(a.path, a.filenameClientConf)
	err = readClientConf(clientConfFilename)
	if err != nil {
		Logger().Warningln("Assets: failed to read ClientConf file: " + err.Error())
	} else {
		Logger().Infoln("Client config successfully read from " + clientConfFilename)
	}
}

// Picks random decoy, returns Server Name Indication and addr in format ipv4:port
func (a *assets) GetDecoyAddress() (sni string, addr string) {
	a.RLock()
	defer a.RUnlock()

	decoys := a.config.GetDecoyList().GetTlsDecoys()
	if len(decoys) == 0 {
		return "", ""
	}
	decoyIndex := getRandInt(0, len(decoys)-1)
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, decoys[decoyIndex].GetIpv4Addr())
	//[TODO]{priority:winter-break}: what checks need to be done, and what's guaranteed?
	addr = ip.To4().String() + ":443"
	sni = decoys[decoyIndex].GetHostname()
	return
}

// Get all Decoys from ClientConf
func (a *assets) GetAllDecoys() []*pb.TLSDecoySpec {
	return a.config.GetDecoyList().GetTlsDecoys()
}

// Get all Decoys from ClientConf that have an IPv6 address
func (a *assets) GetV6Decoys() []*pb.TLSDecoySpec {
	v6Decoys := make([]*pb.TLSDecoySpec, 0)
	allDecoys := a.config.GetDecoyList().GetTlsDecoys()

	for _, decoy := range allDecoys {
		if decoy.GetIpv6Addr() != nil {
			v6Decoys = append(v6Decoys, decoy)
		}
	}

	return v6Decoys
}

// Get all Decoys from ClientConf that have an IPv6 address
func (a *assets) GetV4Decoys() []*pb.TLSDecoySpec {
	v6Decoys := make([]*pb.TLSDecoySpec, 0)
	allDecoys := a.config.GetDecoyList().GetTlsDecoys()

	for _, decoy := range allDecoys {
		if decoy.GetIpv4Addr() != 0 {
			v6Decoys = append(v6Decoys, decoy)
		}
	}

	return v6Decoys
}

// GetDecoy - Gets random DecoySpec
func (a *assets) GetDecoy() pb.TLSDecoySpec {
	a.RLock()
	defer a.RUnlock()

	decoys := a.config.GetDecoyList().GetTlsDecoys()
	chosenDecoy := pb.TLSDecoySpec{}
	if len(decoys) == 0 {
		return chosenDecoy
	}
	decoyIndex := getRandInt(0, len(decoys)-1)
	chosenDecoy = *decoys[decoyIndex]

	//[TODO]{priority:soon} stop enforcing values >= defaults.
	// Fix ackhole instead
	// No value checks when using
	if chosenDecoy.GetTimeout() < timeoutMin {
		timeout := uint32(timeoutMax)
		chosenDecoy.Timeout = &timeout
	}
	if chosenDecoy.GetTcpwin() < sendLimitMin {
		tcpWin := uint32(sendLimitMax)
		chosenDecoy.Tcpwin = &tcpWin
	}
	return chosenDecoy
}

// GetDecoy - Gets random IPv6 DecoySpec
func (a *assets) GetV6Decoy() pb.TLSDecoySpec {
	a.RLock()
	defer a.RUnlock()

	decoys := a.GetV6Decoys()
	chosenDecoy := pb.TLSDecoySpec{}
	if len(decoys) == 0 {
		return chosenDecoy
	}
	decoyIndex := getRandInt(0, len(decoys)-1)
	chosenDecoy = *decoys[decoyIndex]

	// No enforcing TCPWIN etc. values because this is conjure only
	return chosenDecoy
}

func (a *assets) GetRoots() *x509.CertPool {
	a.RLock()
	defer a.RUnlock()

	return a.roots
}

func (a *assets) GetPubkey() *[32]byte {
	a.RLock()
	defer a.RUnlock()

	var pKey [32]byte
	copy(pKey[:], a.config.GetDefaultPubkey().GetKey()[:])
	return &pKey
}

func (a *assets) GetConjurePubkey() *[32]byte {
	a.RLock()
	defer a.RUnlock()

	var pKey [32]byte
	copy(pKey[:], a.config.GetConjurePubkey().GetKey()[:])
	return &pKey
}

func (a *assets) GetGeneration() uint32 {
	a.RLock()
	defer a.RUnlock()

	return a.config.GetGeneration()
}

// Set ClientConf generation and store config to disk
func (a *assets) SetGeneration(gen uint32) (err error) {
	a.Lock()
	defer a.Unlock()

	copyGen := gen
	a.config.Generation = &copyGen
	err = a.saveClientConf()
	return
}

// Set Public key and store config to disk
func (a *assets) SetPubkey(pubkey pb.PubKey) (err error) {
	a.Lock()
	defer a.Unlock()

	copyPubkey := pubkey
	a.config.DefaultPubkey = &copyPubkey
	err = a.saveClientConf()
	return
}

// Set ClientConf and store config to disk
func (a *assets) SetClientConf(conf *pb.ClientConf) (err error) {
	a.Lock()
	defer a.Unlock()

	a.config = *conf
	err = a.saveClientConf()
	return
}

// Not goroutine-safe, use at your own risk
func (a *assets) GetClientConfPtr() *pb.ClientConf {
	return &a.config
}

// Overwrite currently used decoys and store config to disk
func (a *assets) SetDecoys(decoys []*pb.TLSDecoySpec) (err error) {
	a.Lock()
	defer a.Unlock()

	if a.config.DecoyList == nil {
		a.config.DecoyList = &pb.DecoyList{}
	}
	a.config.DecoyList.TlsDecoys = decoys
	err = a.saveClientConf()
	return
}

// Checks if decoy is in currently used ClientConf decoys list
func (a *assets) IsDecoyInList(decoy pb.TLSDecoySpec) bool {
	ipv4str := decoy.GetIpAddrStr()
	hostname := decoy.GetHostname()
	for _, d := range a.config.GetDecoyList().GetTlsDecoys() {
		if strings.Compare(d.GetHostname(), hostname) == 0 &&
			strings.Compare(d.GetIpAddrStr(), ipv4str) == 0 {
			return true
		}
	}
	return false
}

func (a *assets) saveClientConf() error {
	buf, err := proto.Marshal(&a.config)
	if err != nil {
		return err
	}
	filename := path.Join(a.path, a.filenameClientConf)
	tmpFilename := path.Join(a.path, "."+a.filenameClientConf+"."+getRandString(5)+".tmp")
	err = ioutil.WriteFile(tmpFilename, buf[:], 0644)
	if err != nil {
		return err
	}

	return os.Rename(tmpFilename, filename)
}

// SetStatsSocksAddr - Provide a socks address for reporting stats from the client in the form "addr:port"
func (a *assets) SetStatsSocksAddr(addr string) {
	a.socksAddr = addr
}
