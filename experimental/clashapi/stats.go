package clashapi

import (
	"net/http"

	"github.com/pasadizo-net/pasadizo/common/trafficcontrol"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

func statsRouter(trafficManager *trafficcontrol.Manager) http.Handler {
	r := chi.NewRouter()
	r.Get("/sources", getSourceStats(trafficManager))
	return r
}

type sourceStatisticsObject trafficcontrol.SourceStatistics

func (s sourceStatisticsObject) MarshalJSON() ([]byte, error) {
	object := map[string]any{
		"sourceIP":    s.Source,
		"upload":      s.Upload,
		"download":    s.Download,
		"connections": s.Connections,
	}
	if s.InboundType != "" {
		object["inbound"] = inboundName(s.InboundType, s.Inbound)
	}
	return json.Marshal(object)
}

func getSourceStats(trafficManager *trafficcontrol.Manager) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var byInbound bool
		switch r.URL.Query().Get("group") {
		case "", "source":
		case "inbound":
			byInbound = true
		default:
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, ErrBadRequest)
			return
		}
		render.JSON(w, r, render.M{
			"sources": common.Map(trafficManager.SourceStatistics(byInbound), func(it trafficcontrol.SourceStatistics) sourceStatisticsObject {
				return sourceStatisticsObject(it)
			}),
		})
	}
}
