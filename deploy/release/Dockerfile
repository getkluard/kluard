# Used inside the Docker release file (kluard-server-docker-linux-<arch>.tar.gz). It builds the image from the
# programs that are already compiled and in this folder, so nothing is built from source and no Go is needed.
# (The Dockerfile in the source repository builds from source; this one is copied to the top of the release file.)
FROM gcr.io/distroless/static-debian12:nonroot
ENV KLUARD_DATA_DIR=/data
COPY kluard /kluard
COPY --chown=65532:65532 data /data
COPY --chown=65532:65532 backup /backup
COPY dist /dist
EXPOSE 4318
ENTRYPOINT ["/kluard"]
