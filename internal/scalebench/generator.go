package scalebench

import (
	"fmt"
	"math"
	"math/rand"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type DatasetConfig struct {
	Chunks     int    `json:"chunks"`
	Dimensions int    `json:"dimensions"`
	Topics     int    `json:"topics"`
	Tenants    int    `json:"tenants"`
	Seed       int64  `json:"seed"`
	Profile    string `json:"profile"`
}

const (
	ProfileEasyV1 = "easy-v1"
	ProfileHardV2 = "hard-v2"
)

type Generator struct {
	config    DatasetConfig
	centroids [][]float64
}

func NewGenerator(config DatasetConfig) (*Generator, error) {
	if config.Chunks <= 0 || config.Dimensions <= 0 || config.Topics <= 0 || config.Tenants <= 0 {
		return nil, fmt.Errorf("chunks, dimensions, topics, and tenants must be positive")
	}
	if config.Chunks < config.Topics {
		return nil, fmt.Errorf("chunks must be greater than or equal to topics")
	}
	if config.Profile == "" {
		config.Profile = ProfileEasyV1
	}
	if config.Profile != ProfileEasyV1 && config.Profile != ProfileHardV2 {
		return nil, fmt.Errorf("unknown dataset profile %q", config.Profile)
	}
	centroids := make([][]float64, config.Topics)
	if config.Profile == ProfileEasyV1 {
		for topic := range centroids {
			random := rand.New(rand.NewSource(config.Seed + int64(topic)*1_000_003))
			centroids[topic] = randomUnitVector(random, config.Dimensions)
		}
	} else {
		const topicsPerCluster = 10
		clusters := (config.Topics + topicsPerCluster - 1) / topicsPerCluster
		clusterCenters := make([][]float64, clusters)
		for cluster := range clusterCenters {
			random := rand.New(rand.NewSource(config.Seed + int64(cluster)*2_000_003))
			clusterCenters[cluster] = randomUnitVector(random, config.Dimensions)
		}
		for topic := range centroids {
			centroid := append([]float64(nil), clusterCenters[topic/topicsPerCluster]...)
			random := rand.New(rand.NewSource(config.Seed + int64(topic)*1_000_003 + 31))
			addNoise(centroid, random, 0.28)
			centroids[topic] = centroid
		}
	}
	return &Generator{config: config, centroids: centroids}, nil
}

func (g *Generator) Config() DatasetConfig { return g.config }

func (g *Generator) QueryVector(topic int) []float64 {
	return append([]float64(nil), g.centroids[topic%g.config.Topics]...)
}

// BenchmarkQueryVector returns deterministic paraphrase-like variants instead
// of repeating the exact topic centroid when queries outnumber topics.
func (g *Generator) BenchmarkQueryVector(queryIndex int) []float64 {
	topic := queryIndex % g.config.Topics
	variant := queryIndex / g.config.Topics
	vector := g.QueryVector(topic)
	if variant == 0 && g.config.Profile != ProfileHardV2 {
		return vector
	}
	random := rand.New(rand.NewSource(g.config.Seed + int64(topic)*97_409 + int64(variant+1)*65_537 + 101))
	magnitude := 0.04
	if g.config.Profile == ProfileHardV2 {
		magnitude = 0.09
	}
	addNoise(vector, random, magnitude)
	return vector
}

func (g *Generator) Tenant(topic int) string {
	return fmt.Sprintf("tenant_%03d", topic%g.config.Tenants)
}

func (g *Generator) Records(start, count int) []milvus.Record {
	if start < 0 {
		start = 0
	}
	end := min(start+count, g.config.Chunks)
	records := make([]milvus.Record, 0, max(0, end-start))
	for index := start; index < end; index++ {
		topic := index % g.config.Topics
		ordinal := index / g.config.Topics
		visibility := "public"
		var tenants, roles []string
		if ordinal == 0 || ordinal%10 >= 7 {
			visibility = "internal"
			tenants = []string{g.Tenant(topic)}
			if ordinal == 0 || ordinal%2 == 0 {
				roles = []string{"admin"}
			} else {
				roles = []string{"viewer"}
			}
		}
		status := "active"
		if ordinal%20 >= 17 {
			status = "deprecated"
		}
		if ordinal%20 == 19 {
			status = "draft"
		}
		records = append(records, milvus.Record{
			ChunkID:    fmt.Sprintf("bench-t%04d-c%04d", topic, ordinal),
			DocumentID: fmt.Sprintf("bench-doc-%06d", index/10),
			Title:      fmt.Sprintf("Topic %04d knowledge", topic),
			Content:    fmt.Sprintf("Deterministic scale benchmark topic=%d ordinal=%d", topic, ordinal),
			TenantID: func() string {
				if visibility == "public" {
					return "public"
				}
				return g.Tenant(topic)
			}(),
			AllowedTenants: tenants,
			AllowedRoles:   roles,
			Product:        fmt.Sprintf("product_%02d", topic%10),
			Version:        "1.0",
			Status:         status,
			Visibility:     visibility,
			Embedding:      g.chunkVector(index, topic, ordinal),
		})
	}
	return records
}

func (g *Generator) chunkVector(index, topic, ordinal int) []float64 {
	vector := append([]float64(nil), g.centroids[topic]...)
	if ordinal == 0 {
		return vector // Deliberately closest and private: an ACL hard negative.
	}
	random := rand.New(rand.NewSource(g.config.Seed + int64(index)*7_919 + 17))
	if g.config.Profile == ProfileHardV2 {
		addNoise(vector, random, 0.22)
	} else {
		for dimension := range vector {
			vector[dimension] += random.NormFloat64() * 0.025
		}
		normalize(vector)
	}
	return vector
}

func addNoise(vector []float64, random *rand.Rand, l2Magnitude float64) {
	standardDeviation := l2Magnitude / math.Sqrt(float64(len(vector)))
	for dimension := range vector {
		vector[dimension] += random.NormFloat64() * standardDeviation
	}
	normalize(vector)
}

func randomUnitVector(random *rand.Rand, dimensions int) []float64 {
	vector := make([]float64, dimensions)
	for index := range vector {
		vector[index] = random.NormFloat64()
	}
	normalize(vector)
	return vector
}

func normalize(vector []float64) {
	var squared float64
	for _, value := range vector {
		squared += value * value
	}
	if squared == 0 {
		return
	}
	scale := 1 / math.Sqrt(squared)
	for index := range vector {
		vector[index] *= scale
	}
}
