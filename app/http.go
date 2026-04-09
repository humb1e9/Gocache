package app

import (
	"net/http"
	"time"

	cache "gocache/internal/cache"

	"github.com/gin-gonic/gin"
)

type getRequest struct {
	Group string `json:"group" form:"group"`
	Key   string `json:"key" form:"key"`
}

type getResponse struct {
	Group string `json:"group"`
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

func newHTTPServer(addr string, defaultGroup string, group *cache.Group) *http.Server {
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())

	router.POST("/api/cache/get", func(c *gin.Context) {
		var req getRequest
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, getResponse{Error: "invalid request body"})
			return
		}

		groupName := req.Group
		if groupName == "" {
			groupName = defaultGroup
		}
		if groupName != defaultGroup {
			c.JSON(http.StatusBadRequest, getResponse{Error: "unknown group"})
			return
		}
		if req.Key == "" {
			c.JSON(http.StatusBadRequest, getResponse{Error: "key is required"})
			return
		}

		view, err := group.Get(c.Request.Context(), req.Key)
		if err != nil {
			c.JSON(http.StatusNotFound, getResponse{
				Group: groupName,
				Key:   req.Key,
				Error: err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, getResponse{
			Group: groupName,
			Key:   req.Key,
			Value: view.String(),
		})
	})

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	return &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 3 * time.Second,
	}
}
