docker run -p 9000:9000 -p 9090:9090 \
  -e "MINIO_ROOT_USER=minioadmin" \
  -e "MINIO_ROOT_PASSWORD=minioadmin" \
  -v /Users/d062748/workspace/boot.dev/learn-file-storage-s3-golang-starter/assets:/data \
  --name "minio" \
  quay.io/minio/minio server /data --console-address ":9090" 
