// Package conf implements parse the taf config.
// Usage:
// After initialization, use obj.GetXXX("/taf/db<ip>") to get the corresponding data structure.
package conf

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"
	"sync"
)

const (
	// Node shows an element is a node
	Node = iota
	// Leaf shows an element is a leaf
	Leaf
)

var (
	whiteSpaceChars = " \n\t"
)

type elem struct {
	kind     int
	name     string
	value    string
	children map[string]*elem
}

func newElem(kind int, name string) *elem {
	return &elem{kind, name, "", make(map[string]*elem)}
}

func (e *elem) setValue(value string) *elem {
	e.value = value
	return e
}

func (e *elem) addChild(name string, child *elem) *elem {
	e.children[name] = child
	return e
}

func (e *elem) findChild(name string) (ret *elem, ok bool) {
	ret, ok = e.children[name]
	return
}

func (e *elem) isNode() bool {
	return e.kind == Node
}

func (e *elem) isLeaf() bool {
	return e.kind == Leaf
}

func (e *elem) toString(h int) string {
	if e.isLeaf() {
		return fmt.Sprintf("\n%s%s:%s", strings.Repeat("\t", h), e.name, e.value)
	}
	ret := fmt.Sprintf("\n%s%s:", strings.Repeat("\t", h), e.name)
	for _, child := range e.children {
		ret += child.toString(h + 1)
	}
	return ret
}

func (e *elem) getElem(pathVec []string) (*elem, error) {
	targetNode := e
	for _, item := range pathVec {
		t, ok := targetNode.findChild(item)
		if !ok {
			return nil, errors.New("not find")
		}
		targetNode = t
	}
	return targetNode, nil
}

func (e *elem) analysisPath(path string) []string {
	pathVec := strings.Split(path, "/")
	lastItem := pathVec[len(pathVec)-1]
	pathVec = pathVec[:len(pathVec)-1]
	lastPair := strings.Split(lastItem, "<")
	if len(lastPair) == 2 {
		pathVec = append(pathVec, lastPair[0])
		pathVec = append(pathVec, strings.Trim(lastPair[1], ">"))
	} else {
		pathVec = append(pathVec, lastItem)
	}
	var ret []string
	for _, item := range pathVec {
		if item != "" {
			ret = append(ret, item)
		}
	}
	return ret
}

// path like /A/B/C or /A/B/C/
func (e *elem) getDomain(path string) ([]string, error) {
	pathVec := e.analysisPath(path)
	var domain []string
	targetNode, err := e.getElem(pathVec)
	if err != nil {
		return domain, err
	}
	for _, child := range targetNode.children {
		if child.isNode() {
			domain = append(domain, child.name)
		}
	}
	return domain, nil
}

func (e *elem) getMap(path string) (map[string]string, error) {
	pathVec := e.analysisPath(path)
	kvMap := make(map[string]string)
	targetNode, err := e.getElem(pathVec)
	if err != nil {
		return kvMap, nil
	}
	for _, child := range targetNode.children {
		if child.isLeaf() {
			kvMap[child.name] = child.value
		}
	}
	return kvMap, nil
}

// path like /A/B/C/<data> or /A/B/C<data>
func (e *elem) getValue(path string) (string, error) {
	pathVec := e.analysisPath(path)
	targetNode, err := e.getElem(pathVec)
	if err != nil {
		return "", err
	}
	return targetNode.value, nil
}

// Conf struct for parse xml-like tars config file.
type Conf struct {
	content []byte        // content for storing data
	mutex   *sync.RWMutex // mutex for multi goroutines
	root    *elem         // root is the root element
}

// New  returns an new Conf struct.
func New() *Conf {
	return &Conf{[]byte{}, new(sync.RWMutex), newElem(Node, "root")}
}

// NewConf returns a new Conf with the fileName
func NewConf(fileName string) (*Conf, error) {
	c := &Conf{[]byte{}, new(sync.RWMutex), newElem(Node, "root")}
	if err := c.InitFromFile(fileName); err != nil {
		return nil, err
	}
	return c, nil
}

// InitFromFile returns error when init config from a file
func (c *Conf) InitFromFile(fileName string) error {
	content, err := ioutil.ReadFile(fileName)
	if err != nil {
		return fmt.Errorf("read file %s error:%v", fileName, err)
	}
	return c.InitFromBytes(content)
}

// InitFromString returns error when init config from a string
func (c *Conf) InitFromString(content string) error {
	return c.InitFromBytes(([]byte)(content))
}

// InitFromBytes returns error when init config from bytes
func (c *Conf) InitFromBytes(content []byte) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.content = content
	xmlDecoder := xml.NewDecoder(bytes.NewReader(c.content))
	var nodeStack []*elem
	nodeStack = append(nodeStack, c.root)
	for {
		currNode := nodeStack[len(nodeStack)-1]
		token, _ := xmlDecoder.Token()
		if token == nil {
			break
		}
		switch t := token.(type) {
		case xml.CharData:
			lineDecoder := bufio.NewScanner(bytes.NewReader(t))
			lineDecoder.Split(bufio.ScanLines)
			for lineDecoder.Scan() {
				line := strings.Trim(lineDecoder.Text(), whiteSpaceChars)
				if len(line) > 0 && line[0] == '#' {
					continue
				}
				kv := strings.SplitN(line, "=", 2)
				if len(kv) != 2 {
					continue
				}
				k, v := strings.Trim(kv[0], whiteSpaceChars), strings.Trim(kv[1], whiteSpaceChars)
				if k == "" {
					continue
				}
				leaf := newElem(Leaf, k)
				leaf.setValue(v)
				currNode.addChild(k, leaf)
			}
		case xml.StartElement:
			nodeName := t.Name.Local
			node, ok := currNode.findChild(nodeName)
			if !ok {
				node = newElem(Node, nodeName)
				currNode.addChild(nodeName, node)
			}
			nodeStack = append(nodeStack, node)
		case xml.EndElement:
			nodeName := t.Name.Local
			if currNode.name != nodeName {
				return fmt.Errorf("xml end not match :%s", nodeName)
			}
			nodeStack = nodeStack[:len(nodeStack)-1]
		}
	}
	return nil
}

// GetStringWithDef returns the value for pointed path, or a default value when error happens
func (c *Conf) GetStringWithDef(path string, defVal string) string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	value, err := c.root.getValue(path)
	if err != nil {
		return defVal
	}
	return value
}

// GetString returns the value for pointed path
func (c *Conf) GetString(path string) string {
	return c.GetStringWithDef(path, "")
}

// GetIntWithDef returns the value as an integer for pointed path, or a default value when error happens
func (c *Conf) GetIntWithDef(path string, defVal int) int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	value, err := c.root.getValue(path)
	if err != nil {
		return defVal
	}
	iValue, err := strconv.Atoi(value)
	if err != nil {
		return defVal
	}
	return iValue
}

// GetInt returns the value as an integer for pointed path
func (c *Conf) GetInt(path string) int {
	return c.GetIntWithDef(path, 0)
}

// GetDomain returns the domain for pointed path
func (c *Conf) GetDomain(path string) []string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	domain, err := c.root.getDomain(path)
	if err != nil {
		return []string{}
	}
	return domain
}

// GetMap returns the key-value as a map for pointed path
func (c *Conf) GetMap(path string) map[string]string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	kvMap, _ := c.root.getMap(path)
	return kvMap
}

// ToString returns the config as a string
func (c *Conf) ToString() string {
	return c.root.toString(0)
}

// GetInt32WithDef get int32 value
func (c *Conf) GetInt32WithDef(path string, defVal int32) int32 {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	value, err := c.root.getValue(path)
	if err != nil {
		return defVal
	}
	iValue, err := strconv.Atoi(value)
	if err != nil {
		return defVal
	}
	return int32(iValue)
}

// GetBoolWithDef get bool value
func (c *Conf) GetBoolWithDef(path string, defVal bool) bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	value, err := c.root.getValue(path)
	if err != nil {
		return defVal
	}
	bValue, err := strconv.ParseBool(value)
	if err != nil {
		return defVal
	}
	return bValue
}
