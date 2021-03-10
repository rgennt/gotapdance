package phantoms

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"net"

	wr "github.com/mroth/weightedrand"
)

type ConjurePhantomSubnet struct {
	Weight  float32
	Subnets []string
}

type SubnetConfig struct {
	WeightedSubnets []ConjurePhantomSubnet
}

// getSubnets - return EITHER all subnet strings as one composite array if we are
//		selecting unweighted, or return the array associated with the (seed) selected
//		array of subnet strings based on the associated weights
func (sc *SubnetConfig) getSubnets(seed []byte, weighted bool) []string {

	var out []string = []string{}

	if weighted {
		// seed random with hkdf derived seed provided by client
		seedInt, err := binary.ReadVarint(bytes.NewBuffer(seed))
		if err != nil {
			return nil
		}
		rand.Seed(seedInt)

		choices := make([]wr.Choice, 0, len(sc.WeightedSubnets))
		for _, cjSubnet := range sc.WeightedSubnets {
			choices = append(choices, wr.Choice{Item: cjSubnet.Subnets, Weight: uint(cjSubnet.Weight)})
		}
		c, _ := wr.NewChooser(choices...)
		out = c.Pick().([]string)
	} else {

		// Use unweighted config for subnets, concat all into one array and return.
		for _, cjSubnet := range sc.WeightedSubnets {
			for _, subnet := range cjSubnet.Subnets {
				out = append(out, subnet)
			}
		}
	}

	return out
}

// SubnetFilter - Filter IP subnets based on whatever to prevent specific subnets from
//		inclusion in choice. See v4Only and v6Only for reference.
type SubnetFilter func([]*net.IPNet) ([]*net.IPNet, error)

func V4Only(obj []*net.IPNet) ([]*net.IPNet, error) {
	var out []*net.IPNet = []*net.IPNet{}

	for _, _net := range obj {
		if ipv4net := _net.IP.To4(); ipv4net != nil {
			out = append(out, _net)
		}
	}
	return out, nil
}

func V6Only(obj []*net.IPNet) ([]*net.IPNet, error) {
	var out []*net.IPNet = []*net.IPNet{}

	for _, _net := range obj {
		if isIPv6(_net){
			out = append(out, _net)
		}
	}
	fmt.Println(out)
	return out, nil
}

func isIPv6(subnet *net.IPNet) (bool){
	if ipv6net := subnet.IP.To16(); ipv6net != nil {
		for i := 0; i < len(ipv6net.String()); i++ {
			switch ipv6net.String()[i] {
			case '.':
			        return false
			case ':':
			        return true
			}
		}
	}
	return false
}

func parseSubnets(phantomSubnets []string) ([]*net.IPNet, error) {
	var subnets []*net.IPNet = []*net.IPNet{}

	if len(phantomSubnets) == 0 {
		return nil, fmt.Errorf("parseSubnets - no subnets provided")
	}

	for _, strNet := range phantomSubnets {
		_, parsedNet, err := net.ParseCIDR(strNet)
		if err != nil {
			return nil, err
		}
		if parsedNet == nil {
			return nil, fmt.Errorf("failed to parse %v as subnet", parsedNet)
		}

		subnets = append(subnets, parsedNet)
	}

	return subnets, nil
	// return nil, fmt.Errorf("parseSubnets not implemented yet")
}

// SelectAddrFromSubnet - given a seed and a CIDR block choose an address.
// 		This is done by generating a seeded random bytes up to teh length of the
//		full address then using the net mask to zero out any bytes that are
//		already specified by the CIDR block. Tde masked random value is then
//		added to the cidr block base giving the final randomly selected address.
func SelectAddrFromSubnet(seed []byte, net1 *net.IPNet) (net.IP, error) {
	bits, addrLen := net1.Mask.Size()

	ipBigInt := &big.Int{}
	if v4net := net1.IP.To4(); v4net != nil {
		ipBigInt.SetBytes(net1.IP.To4())
	} else if v6net := net1.IP.To16(); v6net != nil {
		ipBigInt.SetBytes(net1.IP.To16())
	}

	seedInt, err := binary.ReadVarint(bytes.NewBuffer(seed))
	if err != nil {
		return nil, err
	}

	rand.Seed(seedInt)
	randBytes := make([]byte, addrLen/8)
	_, err = rand.Read(randBytes)
	if err != nil {
		return nil, err
	}
	randBigInt := &big.Int{}
	randBigInt.SetBytes(randBytes)

	mask := make([]byte, addrLen/8)
	for i := 0; i < addrLen/8; i++ {
		mask[i] = 0xff
	}
	maskBigInt := &big.Int{}
	maskBigInt.SetBytes(mask)
	maskBigInt.Rsh(maskBigInt, uint(bits))

	randBigInt.And(randBigInt, maskBigInt)
	ipBigInt.Add(ipBigInt, randBigInt)

	return net.IP(ipBigInt.Bytes()), nil
}

func selectIPAddr(seed []byte, subnets []*net.IPNet) (*net.IP, error) {

	addresses_total := big.NewInt(0)

	type idNet struct {
		min, max big.Int
		net      *net.IPNet
	}
	var idNets []idNet

	for _, _net := range subnets {
		netMaskOnes, _ := _net.Mask.Size()
		if ipv4net := _net.IP.To4(); ipv4net != nil {
			_idNet := idNet{}
			_idNet.min.Set(addresses_total)
			addresses_total.Add(addresses_total, big.NewInt(2).Exp(big.NewInt(2), big.NewInt(int64(32-netMaskOnes)), nil))
			addresses_total.Sub(addresses_total, big.NewInt(1))
			_idNet.max.Set(addresses_total)
			_idNet.net = _net
			idNets = append(idNets, _idNet)
		} else if ipv6net := _net.IP.To16(); ipv6net != nil {
			_idNet := idNet{}
			_idNet.min.Set(addresses_total)
			addresses_total.Add(addresses_total, big.NewInt(2).Exp(big.NewInt(2), big.NewInt(int64(128-netMaskOnes)), nil))
			addresses_total.Sub(addresses_total, big.NewInt(1))
			_idNet.max.Set(addresses_total)
			_idNet.net = _net
			idNets = append(idNets, _idNet)
		} else {
			return nil, fmt.Errorf("failed to parse %v", _net)
		}
	}

	if addresses_total.Cmp(big.NewInt(0)) <= 0 {
		return nil, fmt.Errorf("No valid addresses specified")
	}

	id := &big.Int{}
	id.SetBytes(seed)
	if id.Cmp(addresses_total) > 0 {
		id.Mod(id, addresses_total)
	}

	var result net.IP
	var err error
	for _, _idNet := range idNets {
		if _idNet.max.Cmp(id) >= 0 && _idNet.min.Cmp(id) == -1 {
			result, err = SelectAddrFromSubnet(seed, _idNet.net)
			if err != nil {
				return nil, fmt.Errorf("Failed to chose IP address: %v", err)
			}
		}
	}
	if result == nil {
		return nil, errors.New("let's rewrite the phantom address selector")
	}
	return &result, nil
}

// SelectPhantom - select one phantom IP address based on shared secret
func SelectPhantom(seed []byte, subnets SubnetConfig, transform SubnetFilter, weighted bool) (*net.IP, error) {

	s, err := parseSubnets(subnets.getSubnets(seed, weighted))
	if err != nil {
		return nil, fmt.Errorf("Failed to parse subnets: %v", err)
	}

	if transform != nil {
		s, err = transform(s)
		if err != nil {
			return nil, err
		}
	}

	return selectIPAddr(seed, s)
}

// SelectPhantomUnweighted - select one phantom IP address based on shared secret
func SelectPhantomUnweighted(seed []byte, subnets SubnetConfig, transform SubnetFilter) (*net.IP, error) {
	return SelectPhantom(seed, subnets, transform, false)
}

// SelectPhantomWeighted - select one phantom IP address based on shared secret
func SelectPhantomWeighted(seed []byte, subnets SubnetConfig, transform SubnetFilter) (*net.IP, error) {
	return SelectPhantom(seed, subnets, transform, true)
}
