package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()
	contentType := header.Header.Get("Content-Type")
	mediatype, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid media type", err)
		return
	}
	if mediatype != "image/jpeg" && mediatype != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Unsupported media type", nil)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get video", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You don't own this video", nil)
		return
	}
	contentTypeToExt := strings.Split(mediatype, "/")
	ext := contentTypeToExt[len(contentTypeToExt)-1]

	b := make([]byte, 32)
	randInt, err := rand.Read(b)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't generate random number", err)
		return
	}
	randomPath := base64.RawStdEncoding.EncodeToString(b[:randInt])

	path := filepath.Join(cfg.assetsRoot, randomPath+"."+ext)
	fmt.Printf("saving thumbnail to %s\n", path)
	diskfile, err := os.Create(path)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create file", err)
		return
	}
	defer diskfile.Close()
	_, err = io.Copy(diskfile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't write file", err)
		return
	}

	url := fmt.Sprintf("http://localhost:%s/assets/%s.%s", cfg.port, randomPath, ext)
	fmt.Printf("thumbnail URL: %s\n", url)
	video.ThumbnailURL = &url

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video in db", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}
