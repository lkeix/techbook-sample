package myrouter

import (
	"net/http"
	"sync"
)

type nodeType int

const (
	static nodeType = iota
	param
)

type Node struct {
	isRoot     bool
	prefix     string
	children   []*Node
	paramChild *Node
	parent     *Node
	nodeType   nodeType
	param      *Param
	handlers   map[string]CustomHandler
}

type Param struct {
	key   string
	value string
}

type Context struct {
	params []*Param
	w      http.ResponseWriter
	r      *http.Request
}

func (c *Context) Write(b []byte) {
	c.w.Write(b)
}

func (c *Context) Param(key string) string {
	for _, p := range c.params {
		if p.key == key {
			return p.value
		}
	}
	return ""
}

func (c *Context) Request() *http.Request {
	return c.r
}

type CustomHandler func(*Context)

func newNode(parent *Node, prefix string, nodeType nodeType) *Node {
	return &Node{
		prefix:   prefix,
		parent:   parent,
		children: []*Node{},
		nodeType: nodeType,
		handlers: make(map[string]CustomHandler),
	}
}

func (n *Node) longestCommonChild(prefix string) *Node {
	var nextChild *Node
	maxLcpIndex := 0
	for i := 0; i < len(n.children); i++ {
		lcpIndex := 0
		maxLen := len(n.children[i].prefix)
		if len(prefix) < maxLen {
			maxLen = len(prefix)
		}

		for lcpIndex < maxLen && prefix[lcpIndex] == n.children[i].prefix[lcpIndex] {
			lcpIndex++
		}

		if maxLcpIndex < lcpIndex {
			maxLcpIndex = lcpIndex
			nextChild = n.children[i]
		}
	}

	return nextChild
}

func (n *Node) getParamChild() *Node {
	for i := 0; i < len(n.children); i++ {
		if n.children[i].nodeType == param {
			return n.children[i]
		}
	}
	return nil
}

func (n *Node) RemoveChild(child *Node) {
	for i := 0; i < len(n.children); i++ {
		if n.children[i] == child {
			n.children = n.children[:i+copy(n.children[i:], n.children[i+1:])]
		}
	}
}

type Router struct {
	tree      *Node
	paramsKey *paramsKey
	pool      sync.Pool
}

func NewRouter() *Router {
	return &Router{
		tree: &Node{
			isRoot:   true,
			handlers: nil,
		},
		paramsKey: &paramsKey{},
		pool: sync.Pool{
			New: func() interface{} {
				return &Context{
					params: make([]*Param, 0),
				}
			},
		},
	}
}

func (r *Router) GET(endpoint string, handler CustomHandler) {
	r.insert(http.MethodGet, endpoint, handler)
}

func (r *Router) insert(method, endpoint string, handler CustomHandler) {
	currentNode := r.tree

	for {
		if endpoint == "" {
			break
		}

		if endpoint[0] == ':' {
			j := 0
			for j < len(endpoint) && endpoint[j] != '/' {
				j++
			}
			if child := currentNode.paramChild; child != nil {
				endpoint = endpoint[j:]
				currentNode = child
				continue
			}

			node := newNode(currentNode, endpoint[:j], param)
			key := endpoint[1:j]
			node.param = &Param{
				key: key,
			}
			currentNode.paramChild = node
			nextNode := node
			endpoint = endpoint[j:]
			currentNode = nextNode
			continue
		}

		nextNode := currentNode.longestCommonChild(endpoint)

		j := 0
		for ; j < len(endpoint); j++ {
			if endpoint[j] == ':' {
				break
			}
		}

		if nextNode == nil {
			node := newNode(currentNode, endpoint[:j], static)
			currentNode.children = append(currentNode.children, node)
			currentNode = node
			endpoint = endpoint[j:]
			continue
		}

		lcpIndex := 0
		endpointLen := len(endpoint)
		nodeLen := len(nextNode.prefix)
		maxLen := endpointLen
		if nodeLen < endpointLen {
			maxLen = nodeLen
		}

		for lcpIndex < maxLen && endpoint[lcpIndex] == nextNode.prefix[lcpIndex] {
			lcpIndex++
		}

		if nodeLen == lcpIndex {
			endpoint = endpoint[lcpIndex:]
			currentNode = nextNode
			continue
		}

		parent := nextNode.parent
		// ノードをアップデートする
		node := newNode(parent, endpoint[:lcpIndex], static)

		nextNode.parent = node
		nextNode.prefix = nextNode.prefix[lcpIndex:]
		node.children = append(node.children, nextNode)
		currentNode.children = append(currentNode.children, node)
		currentNode.RemoveChild(nextNode)

		endpoint = endpoint[lcpIndex:]
		currentNode = node
	}

	currentNode.handlers[method] = handler
}

func (r *Router) staticSearch(currentNode *Node, method, endpoint string) (*Node, string) {
	for {
		nextNode := currentNode.longestCommonChild(endpoint)
		if nextNode == nil {
			return currentNode, endpoint
		}

		maxLen := len(nextNode.prefix)
		if len(endpoint) < maxLen {
			maxLen = len(endpoint)
		}

		lcpIndex := 0
		for lcpIndex < maxLen && endpoint[lcpIndex] == nextNode.prefix[lcpIndex] {
			lcpIndex++
		}

		currentNode = nextNode
		endpoint = endpoint[lcpIndex:]
		if len(endpoint) == 0 {
			break
		}
	}
	return currentNode, ""
}

func (r *Router) paramSearch(currentNode *Node, method, endpoint string) (*Node, string) {
	currentNode = currentNode.paramChild
	i := 0
	for i < len(endpoint) && endpoint[i] != '/' {
		i++
	}

	currentNode.param.value = endpoint[:i]
	endpoint = endpoint[i:]

	return currentNode, endpoint
}

func backTrack(n *Node, endpoint string) (*Node, string) {
	for {
		paramChild := n.paramChild
		if paramChild != nil {
			return n, endpoint
		}

		endpoint = n.prefix + endpoint

		n = n.parent
	}
}

func (r *Router) Search(method, endpoint string) (CustomHandler, []*Param) {
	currentNode := r.tree
	var params []*Param

	for {
		currentNode, endpoint = r.staticSearch(currentNode, method, endpoint)
		if endpoint == "" {
			return currentNode.handlers[method], params
		}

		currentNode, endpoint = backTrack(currentNode, endpoint)
		if currentNode == nil {
			break
		}

		currentNode, endpoint = r.paramSearch(currentNode, method, endpoint)
		params = append(params, &Param{
			key:   currentNode.param.key,
			value: currentNode.param.value,
		})
		if endpoint == "" {
			return currentNode.handlers[method], params
		}
	}
	return nil, params
}

type paramsKey struct{}

func PathParam(r *http.Request, key string) string {
	ctx := r.Context()
	params, ok := ctx.Value(paramsKey{}).([]*Param)
	if !ok {
		return ""
	}

	for _, p := range params {
		if p.key == key {
			return p.value
		}
	}
	return ""
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	ctx := r.pool.Get().(*Context)
	handler, params := r.Search(req.Method, req.URL.Path)
	ctx.r = req
	ctx.w = w
	ctx.params = params
	if handler != nil {
		handler(ctx)
		r.pool.Put(ctx)
		return
	}

	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte("404 Not Found"))
	r.pool.Put(ctx)
}
