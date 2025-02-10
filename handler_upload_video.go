package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxBytes = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	// refactor to function
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}
	// ---

	fmt.Println("uploading video", videoID, "by user", userID)

	// refactor to function
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get video", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You don't own this video", nil)
		return
	}
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	// refactor to function
	contentType := header.Header.Get("Content-Type")
	mediatype, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid media type", err)
		return
	}
	if mediatype != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Unsupported media type", nil)
		return
	}
	// ---

	tempFile, err := os.CreateTemp("", "tubely-video")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create temp file on server", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't write file", err)
		return
	}
	tempFile.Seek(0, io.SeekStart)
	processed_tempfile_name, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't process video for fast start", err)
		return
	}
	newFile, err := os.Open(processed_tempfile_name)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't open processed video file", err)
		return
	}
	defer os.Remove(processed_tempfile_name)
	defer newFile.Close()

	fileInfo, err := newFile.Stat()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get file info", err)
		return
	}
	fileSize := fileInfo.Size()
	newFile.Seek(0, io.SeekStart)

	ratio, err := getVideoAspectRatio(processed_tempfile_name)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get video aspect ratio", err)
		return
	}
	var prefix string
	switch ratio {
	case "16:9":
		prefix = "landscape"
	case "9:16":
		prefix = "portrait"
	default:
		prefix = "other"
	}

	// refactor to function
	contentTypeToExt := strings.Split(mediatype, "/")
	ext := contentTypeToExt[len(contentTypeToExt)-1]

	// refactor to function
	b := make([]byte, 32)
	randInt, err := rand.Read(b)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't generate random number", err)
		return
	}
	randomPath := base64.RawStdEncoding.EncodeToString(b[:randInt])
	randomPath = strings.Replace(randomPath, "/", "", -1)
	fmt.Println("randomPath: " + randomPath)
	fmt.Printf("filesize: %d\n", &fileSize)
	fmt.Printf("ext: %s\n", ext)

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:        aws.String(cfg.s3Bucket),
		Key:           aws.String(fmt.Sprintf("%s/%s.%s", prefix, randomPath, ext)),
		Body:          newFile,
		ContentType:   aws.String(mediatype),
		ContentLength: &fileSize,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't upload video to S3", err)
		return
	}

	// ---
	url := fmt.Sprintf("%s/%s/%s/%s.%s", cfg.s3Endpoint, cfg.s3Bucket, prefix, randomPath, ext)
	fmt.Printf("video URL: %s\n", url)
	video.VideoURL = &url

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video in db", err)
		return
	}

}

type Stream struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type FFProbeOutput struct {
	Streams []Stream `json:"streams"`
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	buf := bytes.Buffer{}
	cmd.Stdout = &buf
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	var output FFProbeOutput
	err = json.Unmarshal(buf.Bytes(), &output)
	if err != nil {
		return "", err
	}

	if len(output.Streams) == 0 {
		return "", fmt.Errorf("no streams found in ffprobe output")
	}

	width := output.Streams[0].Width
	height := output.Streams[0].Height

	if width == 0 || height == 0 {
		return "", fmt.Errorf("invalid width or height in ffprobe output")
	}

	aspectRatio := float64(width) / float64(height)
	fmt.Printf("Debug: width=%d, height=%d, ratio=%f\n", width, height, aspectRatio)
	const tolerance = 0.10
	const ratio16_9 = 16.0 / 9.0
	const ratio9_16 = 9.0 / 16.0

	if math.Abs(aspectRatio-ratio16_9) < tolerance {
		return "16:9", nil
	} else if math.Abs(aspectRatio-ratio9_16) < tolerance {
		return "9:16", nil
	} else {
		return "other", nil
	}
}

func processVideoForFastStart(filePath string) (string, error) {
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", filePath+".processed.mp4")
	// Capture standard error for more details
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("ffmpeg failed with error: %v, stderr: %s", err, stderr.String())
	}
	return filePath + ".processed.mp4", nil
}
