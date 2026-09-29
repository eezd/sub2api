package service

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

const (
	modelTraceValueMin                = 1
	modelTraceValueMax                = 355
	modelTraceDimension               = modelTraceValueMax - modelTraceValueMin + 1
	modelTraceAlpha                   = 0.5
	modelTraceOrderedFeatureDimension = 74
	modelTraceBankSourceSHA256        = "1c2cb74d372f9f0f30d0dabbb7b7a838660d2f769a88d0c8489e4c662e088c21"
	modelTraceDisclaimer              = "ModelTrace 是闭集统计归因，不是模型身份的确定性证明。网关包装、系统提示词、采样策略和模型更新都可能影响结果。"
)

// modeltrace_unified_bank.json.gz is the unified GPT/Claude reference bank from
// /srv/ModelTrace, built 2026-09-23. Keep the source digest above in sync when
// replacing the bank so a release can identify the exact reference artifact.
//
//go:embed modeltrace_unified_bank.json.gz
var modelTraceBankGZIP []byte

var (
	modelTraceBankOnce  sync.Once
	modelTraceBankValue *modelTraceBank
	modelTraceBankErr   error
	modelTraceDigits    = regexp.MustCompile(`[0-9]+`)
)

type modelTraceBank struct {
	BuiltAt string `json:"built_at"`
	Method  struct {
		Name string `json:"name"`
	} `json:"method"`
	Models      []modelTraceBankModel            `json:"models"`
	Robust      modelTraceRobustBank             `json:"robust"`
	Calibration map[string]modelTraceCalibration `json:"calibration"`
}

type modelTraceBankModel struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	Family      string    `json:"family"`
	FamilyName  string    `json:"family_name"`
	Counts      []float64 `json:"counts"`
}

type modelTraceRobustBank struct {
	ModelOrder    []string              `json:"model_order"`
	Hellinger     modelTraceFeatureBank `json:"hellinger"`
	OrderedBlocks modelTraceOrderedBank `json:"ordered_blocks"`
}

type modelTraceFeatureBank struct {
	FeatureMean   []float64   `json:"feature_mean"`
	FeatureScale  []float64   `json:"feature_scale"`
	NuisanceBasis [][]float64 `json:"nuisance_basis"`
	Centroids     [][]float64 `json:"centroids"`
}

type modelTraceOrderedBank struct {
	Weight               float64       `json:"weight"`
	FeatureMean          []float64     `json:"feature_mean"`
	FeatureScale         []float64     `json:"feature_scale"`
	EnvironmentCentroids [][][]float64 `json:"environment_centroids"`
	NuisanceBasis        [][]float64   `json:"nuisance_basis"`
	Centroids            [][]float64   `json:"centroids"`
}

type modelTraceCalibration struct {
	Beta       float64 `json:"beta"`
	CVAccuracy float64 `json:"cv_accuracy"`
}

type modelTraceOutput struct {
	Text          string
	ExpectedCount int
}

// ModelTraceCandidate is one closed-set attribution candidate.
type ModelTraceCandidate struct {
	Model                  string  `json:"model"`
	DisplayName            string  `json:"display_name"`
	Family                 string  `json:"family"`
	FamilyName             string  `json:"family_name"`
	Probability            float64 `json:"probability"`
	ConditionalProbability float64 `json:"conditional_probability"`
	ProfileSimilarity      float64 `json:"profile_similarity"`
	Score                  float64 `json:"score"`
}

// ModelTraceFamilyProbability is the aggregate probability for one model family.
type ModelTraceFamilyProbability struct {
	Family      string  `json:"family"`
	DisplayName string  `json:"display_name"`
	Probability float64 `json:"probability"`
}

// ModelTraceDiagnostic describes one response accepted by the scorer.
type ModelTraceDiagnostic struct {
	Index          int  `json:"index"`
	ParsedNumbers  int  `json:"parsed_numbers"`
	MinimumNumbers int  `json:"minimum_numbers"`
	Accepted       bool `json:"accepted"`
}

// ModelTraceAPITest summarizes the upstream probes used for one attribution.
type ModelTraceAPITest struct {
	Requested   int      `json:"requested"`
	Attempted   int      `json:"attempted"`
	MaxAttempts int      `json:"max_attempts"`
	Received    int      `json:"received"`
	Errors      []string `json:"errors"`
}

// ModelTraceResult is the final model fingerprint attribution returned over SSE.
type ModelTraceResult struct {
	RequestedModel        string                        `json:"requested_model"`
	TestedModel           string                        `json:"tested_model"`
	ExpectedModelInBank   bool                          `json:"expected_model_in_bank"`
	MatchesExpected       *bool                         `json:"matches_expected"`
	Prediction            string                        `json:"prediction"`
	PredictionName        string                        `json:"prediction_name"`
	Probability           float64                       `json:"probability"`
	FamilyPrediction      string                        `json:"family_prediction"`
	FamilyPredictionName  string                        `json:"family_prediction_name"`
	FamilyProbability     float64                       `json:"family_probability"`
	UsedOutputs           int                           `json:"used_outputs"`
	Candidates            []ModelTraceCandidate         `json:"results"`
	FamilyProbabilities   []ModelTraceFamilyProbability `json:"family_probabilities"`
	Diagnostics           []ModelTraceDiagnostic        `json:"diagnostics"`
	CalibrationQueries    int                           `json:"calibration_queries"`
	CalibrationBeta       float64                       `json:"calibration_beta"`
	CalibrationCVAccuracy float64                       `json:"calibration_cv_accuracy"`
	Method                string                        `json:"method"`
	BankBuiltAt           string                        `json:"bank_built_at"`
	BankSourceSHA256      string                        `json:"bank_source_sha256"`
	Disclaimer            string                        `json:"disclaimer"`
	APITest               ModelTraceAPITest             `json:"api_test"`
}

func loadModelTraceBank() (*modelTraceBank, error) {
	modelTraceBankOnce.Do(func() {
		reader, err := gzip.NewReader(bytes.NewReader(modelTraceBankGZIP))
		if err != nil {
			modelTraceBankErr = fmt.Errorf("open embedded ModelTrace bank: %w", err)
			return
		}
		defer func() { _ = reader.Close() }()

		bank := &modelTraceBank{}
		if err := json.NewDecoder(reader).Decode(bank); err != nil && !errors.Is(err, io.EOF) {
			modelTraceBankErr = fmt.Errorf("decode embedded ModelTrace bank: %w", err)
			return
		}
		if err := validateModelTraceBank(bank); err != nil {
			modelTraceBankErr = err
			return
		}
		modelTraceBankValue = bank
	})
	return modelTraceBankValue, modelTraceBankErr
}

func validateModelTraceBank(bank *modelTraceBank) error {
	if bank == nil || len(bank.Models) == 0 {
		return errors.New("embedded ModelTrace bank has no models")
	}
	modelCount := len(bank.Models)
	if len(bank.Robust.ModelOrder) != modelCount {
		return errors.New("embedded ModelTrace bank model order is inconsistent")
	}
	if len(bank.Robust.Hellinger.FeatureMean) != modelTraceDimension ||
		len(bank.Robust.Hellinger.FeatureScale) != modelTraceDimension ||
		len(bank.Robust.Hellinger.Centroids) != modelCount {
		return errors.New("embedded ModelTrace Hellinger dimensions are inconsistent")
	}
	if len(bank.Robust.OrderedBlocks.FeatureMean) != modelTraceOrderedFeatureDimension ||
		len(bank.Robust.OrderedBlocks.FeatureScale) != modelTraceOrderedFeatureDimension ||
		len(bank.Robust.OrderedBlocks.Centroids) != modelCount ||
		len(bank.Robust.OrderedBlocks.EnvironmentCentroids) == 0 {
		return errors.New("embedded ModelTrace ordered-block dimensions are inconsistent")
	}
	for index, model := range bank.Models {
		if model.ID == "" || bank.Robust.ModelOrder[index] != model.ID || len(model.Counts) != modelTraceDimension {
			return errors.New("embedded ModelTrace model metadata is inconsistent")
		}
	}
	for _, key := range []string{"1", "2", "3"} {
		if calibration, ok := bank.Calibration[key]; !ok || calibration.Beta <= 0 {
			return fmt.Errorf("embedded ModelTrace calibration %s is missing", key)
		}
	}
	return nil
}

func parseModelTraceNumbers(text string) []int {
	matches := modelTraceDigits.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}

	current := make([]int, 0, len(matches))
	best := make([]int, 0, len(matches))
	previousEnd := 0
	for _, match := range matches {
		separator := text[previousEnd:match[0]]
		if len(current) > 0 && strings.IndexFunc(separator, unicode.IsLetter) >= 0 {
			if len(current) > len(best) {
				best = append(best[:0], current...)
			}
			current = current[:0]
		}
		value, err := strconv.Atoi(text[match[0]:match[1]])
		if err == nil && value >= modelTraceValueMin && value <= modelTraceValueMax {
			current = append(current, value)
		}
		previousEnd = match[1]
	}
	if len(current) > len(best) {
		best = append(best[:0], current...)
	}
	return best
}

func analyzeModelTraceOutputs(outputs []modelTraceOutput, bank *modelTraceBank) (*ModelTraceResult, error) {
	if bank == nil {
		return nil, errors.New("ModelTrace bank is unavailable")
	}

	type validOutput struct {
		counts []int
		scores []float64
	}
	valid := make([]validOutput, 0, len(outputs))
	diagnostics := make([]ModelTraceDiagnostic, 0, len(outputs))
	for index, output := range outputs {
		numbers := parseModelTraceNumbers(output.Text)
		minimum := 80
		if output.ExpectedCount > 0 {
			minimum = max(minimum, int(math.Ceil(float64(output.ExpectedCount)*0.55)))
		}
		accepted := len(numbers) >= minimum
		diagnostics = append(diagnostics, ModelTraceDiagnostic{
			Index:          index,
			ParsedNumbers:  len(numbers),
			MinimumNumbers: minimum,
			Accepted:       accepted,
		})
		if !accepted {
			continue
		}
		counts := countModelTraceNumbers(numbers)
		scores, err := robustModelTraceScores(numbers, counts, bank)
		if err != nil {
			return nil, err
		}
		valid = append(valid, validOutput{counts: counts, scores: scores})
	}
	if len(valid) == 0 {
		return nil, errors.New("没有可用回答：模型拒答或输出被严重截断")
	}

	modelCount := len(bank.Models)
	combinedScores := make([]float64, modelCount)
	pooledCounts := make([]int, modelTraceDimension)
	for _, output := range valid {
		for index, score := range output.scores {
			combinedScores[index] += score
		}
		for index, count := range output.counts {
			pooledCounts[index] += count
		}
	}
	for index := range combinedScores {
		combinedScores[index] /= float64(len(valid))
	}

	calibrationQueries := min(len(valid), 3)
	calibration := bank.Calibration[strconv.Itoa(calibrationQueries)]
	logits := make([]float64, modelCount)
	for index, score := range combinedScores {
		logits[index] = calibration.Beta * score
	}
	probabilities := modelTraceSoftmax(logits)

	candidates := make([]ModelTraceCandidate, modelCount)
	familyTotals := make(map[string]float64, 2)
	familyNames := make(map[string]string, 2)
	familyOrder := make([]string, 0, 2)
	for index, model := range bank.Models {
		family := model.Family
		if family == "" {
			family = "models"
		}
		familyName := model.FamilyName
		if familyName == "" {
			familyName = family
		}
		if _, exists := familyTotals[family]; !exists {
			familyOrder = append(familyOrder, family)
			familyNames[family] = familyName
		}
		familyTotals[family] += probabilities[index]
		candidates[index] = ModelTraceCandidate{
			Model:             model.ID,
			DisplayName:       model.DisplayName,
			Family:            family,
			FamilyName:        familyName,
			Probability:       probabilities[index],
			ProfileSimilarity: modelTraceJSSimilarity(pooledCounts, model.Counts),
			Score:             combinedScores[index],
		}
	}
	for index := range candidates {
		familyProbability := familyTotals[candidates[index].Family]
		if familyProbability > 0 {
			candidates[index].ConditionalProbability = candidates[index].Probability / familyProbability
		}
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		return candidates[left].Probability > candidates[right].Probability
	})

	familyProbabilities := make([]ModelTraceFamilyProbability, 0, len(familyOrder))
	winningFamily := ""
	for _, family := range familyOrder {
		probability := familyTotals[family]
		familyProbabilities = append(familyProbabilities, ModelTraceFamilyProbability{
			Family:      family,
			DisplayName: familyNames[family],
			Probability: probability,
		})
		if winningFamily == "" || probability > familyTotals[winningFamily] {
			winningFamily = family
		}
	}

	winner := candidates[0]
	return &ModelTraceResult{
		Prediction:            winner.Model,
		PredictionName:        winner.DisplayName,
		Probability:           winner.Probability,
		FamilyPrediction:      winningFamily,
		FamilyPredictionName:  familyNames[winningFamily],
		FamilyProbability:     familyTotals[winningFamily],
		UsedOutputs:           len(valid),
		Candidates:            candidates,
		FamilyProbabilities:   familyProbabilities,
		Diagnostics:           diagnostics,
		CalibrationQueries:    calibrationQueries,
		CalibrationBeta:       calibration.Beta,
		CalibrationCVAccuracy: calibration.CVAccuracy,
		Method:                bank.Method.Name,
		BankBuiltAt:           bank.BuiltAt,
		BankSourceSHA256:      modelTraceBankSourceSHA256,
		Disclaimer:            modelTraceDisclaimer,
	}, nil
}

func robustModelTraceScores(numbers []int, counts []int, bank *modelTraceBank) ([]float64, error) {
	marginal, err := modelTraceHellingerScores(counts, bank.Robust.Hellinger)
	if err != nil {
		return nil, err
	}
	orderedBank := bank.Robust.OrderedBlocks
	if orderedBank.Weight == 0 {
		return marginal, nil
	}
	ordered, err := modelTraceOrderedScores(numbers, orderedBank, len(bank.Models))
	if err != nil {
		return nil, err
	}
	for index := range marginal {
		marginal[index] = (1-orderedBank.Weight)*marginal[index] + orderedBank.Weight*ordered[index]
	}
	return marginal, nil
}

func modelTraceHellingerScores(counts []int, artifact modelTraceFeatureBank) ([]float64, error) {
	if len(counts) != modelTraceDimension {
		return nil, errors.New("invalid ModelTrace count dimension")
	}
	total := modelTraceAlpha * modelTraceDimension
	for _, count := range counts {
		total += float64(count)
	}
	feature := make([]float64, modelTraceDimension)
	for index, count := range counts {
		feature[index] = math.Sqrt((float64(count) + modelTraceAlpha) / total)
		feature[index] = (feature[index] - artifact.FeatureMean[index]) / nonZeroModelTraceScale(artifact.FeatureScale[index])
	}
	removeModelTraceNuisance(feature, artifact.NuisanceBasis)
	normalizeModelTraceVector(feature)

	scores := make([]float64, len(artifact.Centroids))
	for index, centroid := range artifact.Centroids {
		if len(centroid) != len(feature) {
			return nil, errors.New("invalid ModelTrace Hellinger centroid dimension")
		}
		scores[index] = modelTraceDot(feature, centroid)
	}
	standardizeModelTraceVector(scores)
	standardizeModelTraceVector(scores)
	return scores, nil
}

func modelTraceOrderedScores(numbers []int, artifact modelTraceOrderedBank, modelCount int) ([]float64, error) {
	feature := modelTraceOrderedFeature(numbers)
	for index := range feature {
		feature[index] = (feature[index] - artifact.FeatureMean[index]) / nonZeroModelTraceScale(artifact.FeatureScale[index])
	}

	normalized := append([]float64(nil), feature...)
	normalizeModelTraceVector(normalized)
	templateScores := make([]float64, modelCount)
	for index := range templateScores {
		templateScores[index] = math.Inf(-1)
	}
	for _, environment := range artifact.EnvironmentCentroids {
		if len(environment) != modelCount {
			return nil, errors.New("invalid ModelTrace environment centroid dimension")
		}
		for index, centroid := range environment {
			if len(centroid) != len(normalized) {
				return nil, errors.New("invalid ModelTrace ordered centroid dimension")
			}
			score := modelTraceDot(normalized, centroid)
			if score > templateScores[index] {
				templateScores[index] = score
			}
		}
	}
	standardizeModelTraceVector(templateScores)

	projected := append([]float64(nil), feature...)
	removeModelTraceNuisance(projected, artifact.NuisanceBasis)
	normalizeModelTraceVector(projected)
	nuisanceScores := make([]float64, modelCount)
	for index, centroid := range artifact.Centroids {
		if len(centroid) != len(projected) {
			return nil, errors.New("invalid ModelTrace ordered nuisance centroid dimension")
		}
		nuisanceScores[index] = modelTraceDot(projected, centroid)
	}
	standardizeModelTraceVector(nuisanceScores)

	for index := range templateScores {
		templateScores[index] = 0.5*templateScores[index] + 0.5*nuisanceScores[index]
	}
	standardizeModelTraceVector(templateScores)
	return templateScores, nil
}

func modelTraceOrderedFeature(numbers []int) []float64 {
	feature := make([]float64, modelTraceOrderedFeatureDimension)
	baseSize := len(numbers) / 4
	remainder := len(numbers) % 4
	start := 0
	for chunk := 0; chunk < 4; chunk++ {
		size := baseSize
		if chunk < remainder {
			size++
		}
		end := start + size
		var histogram [16]int
		for _, value := range numbers[start:end] {
			bucket := int(math.Floor(float64(value-modelTraceValueMin) * 16 / 355))
			if bucket < 0 {
				bucket = 0
			} else if bucket > 15 {
				bucket = 15
			}
			histogram[bucket]++
		}
		denominator := float64(size) + modelTraceAlpha*16
		for bucket, count := range histogram {
			feature[chunk*16+bucket] = math.Sqrt((float64(count) + modelTraceAlpha) / denominator)
		}
		start = end
	}

	var lastDigits [10]int
	for _, value := range numbers {
		lastDigits[value%10]++
	}
	denominator := float64(len(numbers)) + modelTraceAlpha*10
	for digit, count := range lastDigits {
		feature[64+digit] = math.Sqrt((float64(count) + modelTraceAlpha) / denominator)
	}
	return feature
}

func countModelTraceNumbers(numbers []int) []int {
	counts := make([]int, modelTraceDimension)
	for _, number := range numbers {
		counts[number-modelTraceValueMin]++
	}
	return counts
}

func removeModelTraceNuisance(vector []float64, basis [][]float64) {
	for _, axis := range basis {
		if len(axis) != len(vector) {
			continue
		}
		coefficient := modelTraceDot(vector, axis)
		for index := range vector {
			vector[index] -= coefficient * axis[index]
		}
	}
}

func normalizeModelTraceVector(values []float64) {
	squaredNorm := 0.0
	for _, value := range values {
		squaredNorm += value * value
	}
	norm := math.Max(math.Sqrt(squaredNorm), 1e-12)
	for index := range values {
		values[index] /= norm
	}
}

func standardizeModelTraceVector(values []float64) {
	if len(values) == 0 {
		return
	}
	mean := 0.0
	for _, value := range values {
		mean += value
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, value := range values {
		delta := value - mean
		variance += delta * delta
	}
	scale := math.Max(math.Sqrt(variance/float64(len(values))), 1e-12)
	for index := range values {
		values[index] = (values[index] - mean) / scale
	}
}

func modelTraceSoftmax(values []float64) []float64 {
	maximum := values[0]
	for _, value := range values[1:] {
		maximum = math.Max(maximum, value)
	}
	weights := make([]float64, len(values))
	total := 0.0
	for index, value := range values {
		weight := math.Exp(value - maximum)
		weights[index] = weight
		total += weight
	}
	for index := range weights {
		weights[index] /= total
	}
	return weights
}

func modelTraceJSSimilarity(left []int, right []float64) float64 {
	leftTotal := 0.0
	for _, value := range left {
		leftTotal += float64(value)
	}
	if leftTotal == 0 {
		return 0
	}
	rightTotal := modelTraceAlpha * modelTraceDimension
	for _, value := range right {
		rightTotal += value
	}
	divergenceLeft := 0.0
	divergenceRight := 0.0
	for index, value := range left {
		p := float64(value) / leftTotal
		q := (right[index] + modelTraceAlpha) / rightTotal
		midpoint := (p + q) / 2
		if p > 0 {
			divergenceLeft += p * math.Log(p/midpoint)
		}
		if q > 0 {
			divergenceRight += q * math.Log(q/midpoint)
		}
	}
	js := (divergenceLeft + divergenceRight) / 2
	return 1 - math.Sqrt(js/math.Log(2))
}

func modelTraceDot(left, right []float64) float64 {
	total := 0.0
	for index, value := range left {
		total += value * right[index]
	}
	return total
}

func nonZeroModelTraceScale(value float64) float64 {
	if math.Abs(value) >= 1e-12 {
		return value
	}
	return 1e-12
}

func modelTraceBankHasModel(bank *modelTraceBank, modelID string) bool {
	modelID = strings.TrimSpace(strings.ToLower(modelID))
	for _, model := range bank.Models {
		if strings.ToLower(model.ID) == modelID {
			return true
		}
	}
	return false
}
