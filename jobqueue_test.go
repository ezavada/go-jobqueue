package jobqueue

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

const BadgerDBPath = "/tmp/badger"

func init() { //nolint:gochecknoinits // for testing
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
}

type TestJob struct {
	Msg string
}

func (j TestJob) Process(ctx JobContext) error {
	fmt.Println("Test job processed:", j.Msg, ctx.JobID(), ctx.JobCreatedAt().Unix()) //nolint:forbidigo // for testing
	return nil
}

func TestJobQueue_Enqueue(t *testing.T) {
	jq, err := NewJobQueue[TestJob](BadgerDBPath, "test-job", 0)
	assert.NoError(t, err)

	t.Cleanup(func() {
		assert.NoError(t, jq.Stop())
		cleanupBadgerDB(t)
	})

	j := TestJob{Msg: "hello"}

	id, err := jq.Enqueue(j)
	assert.NoError(t, err)

	// Verify that the job was stored in badger DB
	value, err := readKey(jq.db, id)
	assert.NoError(t, err)

	var dbJob job[TestJob]
	err = json.Unmarshal(value, &dbJob)
	assert.NoError(t, err)

	assert.Equal(t, id, dbJob.ID)
	assert.Equal(t, j, dbJob.Payload)
	assert.Equal(t, JobStatusPending, dbJob.Status)
	assert.WithinDuration(t, time.Now(), dbJob.CreatedAt, time.Second)
}

func TestJobQueue_ProcessJob(t *testing.T) {
	jq, err := NewJobQueue[TestJob](BadgerDBPath, "test-job", 0)
	assert.NoError(t, err)

	t.Cleanup(func() {
		assert.NoError(t, jq.Stop())
		cleanupBadgerDB(t)
	})

	// Queue a job
	id, err := jq.Enqueue(TestJob{Msg: "hello"})
	assert.NoError(t, err)

	// Blocks until the job is fetched from badger
	j := <-jq.jobs

	// Check that the job is added to the in-memory index
	assert.Contains(t, jq.isJobIDInQueue, id)

	// Check that the job is what we're expecting
	assert.Equal(t, id, j.ID)
	assert.Equal(t, TestJob{Msg: "hello"}, j.Payload)
	assert.Equal(t, JobStatusPending, j.Status)
	assert.WithinDuration(t, time.Now(), j.CreatedAt, time.Second)

	// Process the job
	assert.NoError(t, jq.processJob(j))

	// Check that the job is removed from the in-memory index
	assert.NotContains(t, jq.isJobIDInQueue, id)

	// Check that the job is no longer in the badger DB
	value, err := readKey(jq.db, id)
	assert.Error(t, err, badger.ErrKeyNotFound)
	assert.Nil(t, value)
}

func TestJobQueue_Recovery(t *testing.T) {
	// Create initial job queue
	jq, err := NewJobQueue[TestJob]("/tmp/badger", "test-job", 0)
	assert.NoError(t, err)

	t.Cleanup(func() {
		cleanupBadgerDB(t)
	})

	// Enqueue job to initial job queue
	id, err := jq.Enqueue(TestJob{Msg: "hello"})
	assert.NoError(t, err)

	// Stop initial job queue
	assert.NoError(t, jq.Stop())

	// Create recovered job queue
	recoveredJq, err := NewJobQueue[TestJob]("/tmp/badger", "test-job", 0)
	assert.NoError(t, err)

	j := <-recoveredJq.jobs

	// Verify that the job is recovered correctly
	assert.Equal(t, id, j.ID)
	assert.Equal(t, j.Payload, TestJob{Msg: "hello"})

	// Process the job in recovered job queue
	assert.NoError(t, recoveredJq.processJob(j))

	// Stop recovered job queue
	assert.NoError(t, recoveredJq.Stop())
}

func readKey(db *badger.DB, key string) ([]byte, error) {
	var valCopy []byte
	err := db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}

		valCopy, err = item.ValueCopy(nil)
		return err
	})

	if err != nil {
		return nil, err
	}

	return valCopy, nil
}

func cleanupBadgerDB(t *testing.T) {
	assert.NoError(t, os.RemoveAll(BadgerDBPath))
}
