package server

import (
	"github.com/gin-gonic/gin"
)

// Codex / OpenAI endpoint handlers. Phase 3 will fill these in:
//   - handleCodexChatCompletions / handleCodexResponses: forward to
//     api.openai.com (API-key) or chatgpt.com/backend-api/codex (OAuth)
//     with the provider-appropriate headers and usage extraction.
//   - handleCodexModels: synthesize the /v1/models listing from the
//     loaded Auth catalog, filtered by plan tier for OAuth credentials.

func (s *Server) handleCodexChatCompletions(c *gin.Context) {
	c.AbortWithStatusJSON(501, gin.H{
		"error": "codex chat/completions not yet implemented (phase 3)",
	})
}

func (s *Server) handleCodexResponses(c *gin.Context) {
	c.AbortWithStatusJSON(501, gin.H{
		"error": "codex responses not yet implemented (phase 3)",
	})
}

func (s *Server) handleCodexModels(c *gin.Context) {
	c.JSON(200, gin.H{"object": "list", "data": []any{}})
}
