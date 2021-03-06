package db

import (
	"log"
	"regexp"
	"testing"
	"time"

	"github.com/couchbaselabs/go.assert"

	"github.com/couchbaselabs/sync_gateway/base"
)

func makeExternalBucket() base.Bucket {
	bucket, err := ConnectToBucket(base.BucketSpec{
		Server:     "walrus:",
		BucketName: "external_bucket"})
	if err != nil {
		log.Fatalf("Couldn't connect to bucket: %v", err)
	}
	return bucket
}

// Evaluates a condition every 100ms until it becomes true. If 3sec elapse, fails an assertion
func waitFor(t *testing.T, condition func() bool) bool {
	var start = time.Now()
	for !condition() {
		if time.Since(start) >= 3*time.Second {
			assertFailed(t, "Timeout!")
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return true
}

func TestShadowerPull(t *testing.T) {
	bucket := makeExternalBucket()
	defer bucket.Close()
	bucket.Set("key1", 0, Body{"foo": 1})
	bucket.Set("key2", 0, Body{"bar": -1})

	db := setupTestDB(t)
	defer tearDownTestDB(t, db)

	shadower, err := NewShadower(db.DatabaseContext, bucket, nil)
	assertNoError(t, err, "NewShadower")
	defer shadower.Stop()

	base.Log("Waiting for shadower to catch up...")
	var doc1, doc2 *document
	waitFor(t, func() bool {
		seq, _ := db.LastSequence()
		return seq >= 2
	})
	doc1, _ = db.GetDoc("key1")
	doc2, _ = db.GetDoc("key2")
	assert.DeepEquals(t, doc1.body, Body{"foo": float64(1)})
	assert.DeepEquals(t, doc2.body, Body{"bar": float64(-1)})

	base.Log("Deleting remote doc")
	bucket.Delete("key1")

	waitFor(t, func() bool {
		seq, _ := db.LastSequence()
		return seq >= 3
	})

	doc1, _ = db.GetDoc("key1")
	assert.True(t, doc1.Deleted)
	_, err = db.Get("key1")
	assert.DeepEquals(t, err, &base.HTTPError{Status: 404, Message: "deleted"})
}

func TestShadowerPush(t *testing.T) {
	//base.LogKeys["Shadow"] = true
	bucket := makeExternalBucket()
	defer bucket.Close()

	db := setupTestDB(t)
	defer tearDownTestDB(t, db)

	var err error
	db.Shadower, err = NewShadower(db.DatabaseContext, bucket, nil)
	assertNoError(t, err, "NewShadower")

	key1rev1, err := db.Put("key1", Body{"aaa": "bbb"})
	assertNoError(t, err, "Put")
	_, err = db.Put("key2", Body{"ccc": "ddd"})
	assertNoError(t, err, "Put")

	base.Log("Waiting for shadower to catch up...")
	var doc1, doc2 Body
	waitFor(t, func() bool {
		return bucket.Get("key1", &doc1) == nil && bucket.Get("key2", &doc2) == nil
	})
	assert.DeepEquals(t, doc1, Body{"aaa": "bbb"})
	assert.DeepEquals(t, doc2, Body{"ccc": "ddd"})

	base.Log("Deleting local doc")
	db.DeleteDoc("key1", key1rev1)

	waitFor(t, func() bool {
		err = bucket.Get("key1", &doc1)
		return err != nil
	})
	assert.True(t, base.IsDocNotFoundError(err))
}

func TestShadowerPattern(t *testing.T) {
	bucket := makeExternalBucket()
	defer bucket.Close()
	bucket.Set("key1", 0, Body{"foo": 1})
	bucket.Set("ignorekey", 0, Body{"bar": -1})
	bucket.Set("key2", 0, Body{"bar": -1})

	db := setupTestDB(t)
	defer tearDownTestDB(t, db)

	pattern, _ := regexp.Compile(`key\d+`)
	shadower, err := NewShadower(db.DatabaseContext, bucket, pattern)
	assertNoError(t, err, "NewShadower")
	defer shadower.Stop()

	base.Log("Waiting for shadower to catch up...")
	waitFor(t, func() bool {
		seq, _ := db.LastSequence()
		return seq >= 1
	})
	doc1, _ := db.GetDoc("key1")
	docI, _ := db.GetDoc("ignorekey")
	doc2, _ := db.GetDoc("key2")
	assert.DeepEquals(t, doc1.body, Body{"foo": float64(1)})
	assert.True(t, docI == nil)
	assert.DeepEquals(t, doc2.body, Body{"bar": float64(-1)})
}
