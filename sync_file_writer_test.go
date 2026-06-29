package adb

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/asjdf/goadb/wire"
	"github.com/stretchr/testify/assert"
)

func TestFileWriterWriteSingleChunk(t *testing.T) {
	var buf bytes.Buffer
	writer := newSyncFileWriter(wire.NewSyncSender(&buf), MtimeOfClose)

	n, err := writer.Write([]byte("hello"))
	assert.NoError(t, err)
	assert.Equal(t, 5, n)

	assert.Equal(t, "DATA\005\000\000\000hello", buf.String())
}

func TestFileWriterWriteMultiChunk(t *testing.T) {
	var buf bytes.Buffer
	writer := newSyncFileWriter(wire.NewSyncSender(&buf), MtimeOfClose)

	n, err := writer.Write([]byte("hello"))
	assert.NoError(t, err)
	assert.Equal(t, 5, n)

	n, err = writer.Write([]byte(" world"))
	assert.NoError(t, err)
	assert.Equal(t, 6, n)

	assert.Equal(t, "DATA\005\000\000\000helloDATA\006\000\000\000 world", buf.String())
}

func TestFileWriterWriteLargeChunk(t *testing.T) {
	var buf bytes.Buffer
	writer := newSyncFileWriter(wire.NewSyncSender(&buf), MtimeOfClose)

	// Send just enough data to get 2 chunks.
	data := make([]byte, wire.SyncMaxChunkSize+1)
	n, err := writer.Write(data)

	assert.NoError(t, err)
	assert.Equal(t, wire.SyncMaxChunkSize+1, n)
	assert.Equal(t, 8+8+wire.SyncMaxChunkSize+1, buf.Len())

	// First header.
	chunk := buf.Bytes()[:8+wire.SyncMaxChunkSize]
	expectedHeader := []byte("DATA----")
	binary.LittleEndian.PutUint32(expectedHeader[4:], wire.SyncMaxChunkSize)
	assert.Equal(t, expectedHeader, chunk[:8])
	assert.Equal(t, data[:wire.SyncMaxChunkSize], chunk[8:])

	// Second header.
	chunk = buf.Bytes()[wire.SyncMaxChunkSize+8 : wire.SyncMaxChunkSize+8+1]
	expectedHeader = []byte("DATA\000\000\000\000")
	binary.LittleEndian.PutUint32(expectedHeader[4:], 1)
	assert.Equal(t, expectedHeader, chunk[:8])
}

func TestFileWriterCloseEmpty(t *testing.T) {
	var buf bytes.Buffer
	mtime := time.Unix(1, 0)
	writer := newSyncFileWriter(wire.NewSyncSender(&buf), mtime)

	assert.NoError(t, writer.Close())

	assert.Equal(t, "DONE\x01\x00\x00\x00", buf.String())
}

func TestFileWriterWriteClose(t *testing.T) {
	var buf bytes.Buffer
	mtime := time.Unix(1, 0)
	writer := newSyncFileWriter(wire.NewSyncSender(&buf), mtime)

	writer.Write([]byte("hello"))
	assert.NoError(t, writer.Close())

	assert.Equal(t, "DATA\005\000\000\000helloDONE\x01\x00\x00\x00", buf.String())
}

func TestSendFileCloseReadsFinalStatus(t *testing.T) {
	conn := &wire.SyncConn{
		SyncScanner: &recordingSyncScanner{status: wire.StatusSuccess},
		SyncSender:  &recordingSyncSender{},
	}
	writer, err := sendFile(conn, "/data/local/tmp/payload", 0o644, time.Unix(1, 0))
	assert.NoError(t, err)

	assert.NoError(t, writer.Close())

	scanner := conn.SyncScanner.(*recordingSyncScanner)
	assert.True(t, scanner.readStatusCalled)
	assert.Equal(t, "send", scanner.request)
}

func TestFileWriterCloseAutoMtime(t *testing.T) {
	var buf bytes.Buffer
	writer := newSyncFileWriter(wire.NewSyncSender(&buf), MtimeOfClose)

	assert.NoError(t, writer.Close())
	assert.Len(t, buf.String(), 8)
	assert.True(t, strings.HasPrefix(buf.String(), "DONE"))

	mtimeBytes := buf.Bytes()[4:]
	mtimeActual := time.Unix(int64(binary.LittleEndian.Uint32(mtimeBytes)), 0)

	// Delta has to be a whole second since adb only supports second granularity for mtimes.
	assert.WithinDuration(t, time.Now(), mtimeActual, 1*time.Second)
}

type recordingSyncScanner struct {
	status           string
	request          string
	readStatusCalled bool
}

func (s *recordingSyncScanner) Close() error {
	return nil
}

func (s *recordingSyncScanner) ReadStatus(req string) (string, error) {
	s.readStatusCalled = true
	s.request = req
	return s.status, nil
}

func (s *recordingSyncScanner) ReadInt32() (int32, error) {
	return 0, io.EOF
}

func (s *recordingSyncScanner) ReadFileMode() (os.FileMode, error) {
	return 0, io.EOF
}

func (s *recordingSyncScanner) ReadTime() (time.Time, error) {
	return time.Time{}, io.EOF
}

func (s *recordingSyncScanner) ReadString() (string, error) {
	return "", io.EOF
}

func (s *recordingSyncScanner) ReadBytes() (io.Reader, error) {
	return nil, io.EOF
}

type recordingSyncSender struct {
	writes bytes.Buffer
}

func (s *recordingSyncSender) Close() error {
	return nil
}

func (s *recordingSyncSender) SendOctetString(value string) error {
	_, err := s.writes.WriteString(value)
	return err
}

func (s *recordingSyncSender) SendInt32(value int32) error {
	return binary.Write(&s.writes, binary.LittleEndian, value)
}

func (s *recordingSyncSender) SendFileMode(mode os.FileMode) error {
	return binary.Write(&s.writes, binary.LittleEndian, mode)
}

func (s *recordingSyncSender) SendTime(value time.Time) error {
	return s.SendInt32(int32(value.Unix()))
}

func (s *recordingSyncSender) SendBytes(data []byte) error {
	if err := s.SendInt32(int32(len(data))); err != nil {
		return err
	}
	_, err := s.writes.Write(data)
	return err
}
