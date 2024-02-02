// go:build integration

package data

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/pressly/goose"
)

var (
	host     = "localhost"
	user     = "postgres"
	password = "postgres"
	dbName   = "polls_test"
	port     = "5434"
	dsn      = "host=%s port=%s user=%s password=%s dbname=%s sslmode=disable"
)

var (
	resource   *dockertest.Resource
	pool       *dockertest.Pool
	testDB     *pgxpool.Pool
	testModels Models
)

func TestMain(m *testing.M) {
	endpoint := os.Getenv("DOCKER_TEST")
	p, err := dockertest.NewPool(endpoint)
	if err != nil {
		log.Fatalf("could not connect to docker: %s", err)
	}

	pool = p

	opts := dockertest.RunOptions{
		Repository: "postgres",
		Env: []string{
			"POSTGRES_USER=" + user,
			"POSTGRES_PASSWORD=" + password,
			"POSTGRES_DB=" + dbName,
		},
		ExposedPorts: []string{"5432"},
		PortBindings: map[docker.Port][]docker.PortBinding{
			"5432": {{HostIP: "0.0.0.0", HostPort: port}},
		},
	}

	resource, err := pool.RunWithOptions(&opts)
	if err != nil {
		_ = pool.Purge(resource)
		log.Fatalf("could not connect to docker: %s", err)
	}

	if err := pool.Retry(func() error {
		var err error
		testDB, err = pgxpool.New(context.Background(), fmt.Sprintf(dsn, host, port, user, password, dbName))
		if err != nil {
			log.Println("Error: ", err)
			return err
		}
		return testDB.Ping(context.Background())
	}); err != nil {
		_ = pool.Purge(resource)
		log.Fatalf("something went wrong: %s", err)
	}

	if err = runMigrations(); err != nil {
		log.Fatalf("something went wrong: %s", err)
	}

	testModels = NewModels(testDB)

	code := m.Run()
	if err := pool.Purge(resource); err != nil {
		log.Fatalf("could not purge resource: %s", err)
	}
	os.Exit(code)
}

func runMigrations() error {
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("createTables: %w", err)
	}

	db := stdlib.OpenDBFromPool(testDB)

	if err := goose.Up(db, "../../migrations"); err != nil {
		return fmt.Errorf("createTables: %w", err)
	}

	return nil
}

func Test_pingDB(t *testing.T) {
	err := testDB.Ping(context.Background())
	if err != nil {
		t.Error("can't ping DB")
	}
}

func TestPollsInsert(t *testing.T) {
	poll := Poll{
		Question: "Test?",
		Options: []*PollOption{
			{Value: "One", Position: 0},
			{Value: "Two", Position: 1},
			{Value: "Three", Position: 2},
		},
	}

	if err := testModels.Polls.Insert(&poll); err != nil {
		t.Errorf("insert poll returned an error: %s", err)
	}

	if poll.ID != 1 {
		t.Errorf("expected id to be 1 but got %d", poll.ID)
	}

	if poll.CreatedAt.IsZero() || poll.UpdatedAt.IsZero() {
		t.Errorf("expected created and updated not to be zero values")
	}

	for _, opt := range poll.Options {
		if opt.ID == 0 {
			t.Errorf("expected option id not to be zero: %s %d", opt.Value, opt.ID)
		}
	}
}

func TestPollsGet(t *testing.T) {
	poll, err := testModels.Polls.Get(1)
	if err != nil {
		t.Errorf("get poll returned an error: %s", err)
	}

	if poll.Question != "Test?" {
		t.Errorf("get poll returned wrong question: expected 'Test?' but got %s", poll.Question)
	}

	_, err = testModels.Polls.Get(9)
	if !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("expected error on non-existent poll")
	}

	_, err = testModels.Polls.Get(0)
	if !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("expected error on bad poll id")
	}
}

func TestPollsUpdate(t *testing.T) {
	newQuestion := "Is this a test?"
	newDescription := "Test description."
	newExpires := ExpiresAt{time.Now().Add(10 * time.Minute)}

	poll, _ := testModels.Polls.Get(1)

	oldUpdatedAt := poll.UpdatedAt

	poll.Question = newQuestion
	poll.Description = newDescription
	poll.ExpiresAt = newExpires

	time.Sleep(1 * time.Second)
	if err := testModels.Polls.Update(poll); err != nil {
		t.Errorf("update poll returned an error: %s", err)
	}

	poll, _ = testModels.Polls.Get(1)

	if poll.Question != newQuestion {
		t.Errorf("expected question to be %s, but got %s", newQuestion, poll.Question)
	}
	if poll.Description != newDescription {
		t.Errorf("expected description to be %s, but got %s", newDescription, poll.Description)
	}
	if poll.ExpiresAt.IsZero() {
		t.Errorf("expected expires at not to be zero value")
	}

	if poll.UpdatedAt.Equal(oldUpdatedAt) {
		t.Errorf("expected updated at to be changed")
	}
}

func TestPollOptionsInsert(t *testing.T) {
	poll, _ := testModels.Polls.Get(1)
	oldUpdatedAt := poll.UpdatedAt

	newValue := "Four"

	option := PollOption{
		Value:    newValue,
		Position: 3,
	}

	time.Sleep(1 * time.Second)
	if err := testModels.PollOptions.Insert(&option, 1); err != nil {
		t.Errorf("add option returned an error: %s", err)
	}

	poll, _ = testModels.Polls.Get(1)

	if len(poll.Options) != 4 {
		t.Errorf("expected 4 options in poll, but got %d", len(poll.Options))
	}

	match := false
	for _, opt := range poll.Options {
		if opt.Value == newValue {
			match = true
		}
	}
	if !match {
		t.Errorf("expected option to contain value %q, but it doesn't", newValue)
	}

	if poll.UpdatedAt.Equal(oldUpdatedAt) {
		t.Errorf("expected poll updated at to be changed")
	}
}

func TestPollOptionsUpdateValue(t *testing.T) {
	newValue := "Test change value"

	option := PollOption{
		ID:    1,
		Value: newValue,
	}

	if err := testModels.PollOptions.UpdateValue(&option); err != nil {
		t.Errorf("update option value returned an error: %s", err)
	}

	poll, _ := testModels.Polls.Get(1)

	match := false
	for _, opt := range poll.Options {
		if opt.ID == 1 && opt.Value == newValue {
			match = true
		}
	}

	if !match {
		t.Errorf("option value not updated")
	}
}

func TestPollOptionsUpdatePosition(t *testing.T) {
	options := []*PollOption{
		{ID: 4, Position: 2},
		{ID: 3, Position: 3},
	}

	if err := testModels.PollOptions.UpdatePosition(options); err != nil {
		t.Errorf("update option value returned an error: %s", err)
	}

	poll, _ := testModels.Polls.Get(1)

	for _, opt := range poll.Options {
		if opt.Value == "Four" {
			if opt.Position != 2 {
				t.Errorf(
					"option %s did not change position: want 2 but got %d",
					opt.Value,
					opt.Position,
				)
			}
		}
		if opt.Value == "Three" {
			if opt.Position != 3 {
				t.Errorf(
					"option %s did not change position: want 2 but got %d",
					opt.Value,
					opt.Position,
				)
			}
		}
	}
}

func TestPollOptionsVote(t *testing.T) {
	err := testModels.PollOptions.Vote(1)
	if err != nil {
		t.Errorf("vote option returned an error: %s", err)
	}

	poll, _ := testModels.Polls.Get(1)
	for _, opt := range poll.Options {
		if opt.ID == 1 && opt.VoteCount != 1 {
			t.Errorf("expected vote count to increase by one, but it didn't: vote_count %d", opt.VoteCount)
		}
	}

	_ = testModels.PollOptions.Vote(1)
	_ = testModels.PollOptions.Vote(1)
	poll, _ = testModels.Polls.Get(1)
	for _, opt := range poll.Options {
		if opt.ID == 1 && opt.VoteCount != 3 {
			t.Errorf("expected vote count to be 3, but got %d", opt.VoteCount)
		}
	}

	if err := testModels.PollOptions.Vote(99); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("expected error on non-existent option")
	}
}

func TestPollOptionsDelete(t *testing.T) {
	if err := testModels.PollOptions.Delete(3); err != nil {
		t.Errorf("delete option value returned an error: %s", err)
	}

	poll, _ := testModels.Polls.Get(1)

	if len(poll.Options) != 3 {
		t.Errorf("expected len of options to be 3 but got %d", len(poll.Options))
	}

	if err := testModels.PollOptions.Delete(5); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("expected error on non-existent option")
	}
}

func TestPollsDelete(t *testing.T) {
	if err := testModels.Polls.Delete(10); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("expected error on non-existent poll")
	}
	if err := testModels.Polls.Delete(0); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("expected error on bad poll id")
	}

	if err := testModels.Polls.Delete(1); err != nil {
		t.Errorf("delete poll returned an error: %s", err)
	}
	_, err := testModels.Polls.Get(1)
	if !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("expected error on getting deleted poll")
	}
}

func TestPollGetAll(t *testing.T) {
	var poll Poll
	for i := 1; i <= 10; i++ {
		if i == 10 {
			// sleep to delay inserting the record
			time.Sleep(time.Second)
		}
		poll.Question = fmt.Sprintf("%c question", 96+i)
		poll.Options = []*PollOption{
			{Value: fmt.Sprintf("Option one, poll %c", 96+i), Position: 0},
			{Value: fmt.Sprintf("Option two, poll %c", 96+i), Position: 1},
			{Value: fmt.Sprintf("Option three, poll %c", 96+i), Position: 2},
		}
		if err := testModels.Polls.Insert(&poll); err != nil {
			t.Fatalf("get all polls - insert poll returned an error: %s", err)
		}
	}

	tests := []struct {
		name             string
		search           string
		page             int
		pageSize         int
		sort             string
		expectedRecords  int
		expectedTotal    int
		expectedLastPage int
	}{
		{
			name:             "default settings",
			page:             1,
			pageSize:         20,
			sort:             "-created_at",
			expectedRecords:  10,
			expectedTotal:    10,
			expectedLastPage: 1,
		},
		{
			name:             "page size",
			page:             1,
			pageSize:         2,
			sort:             "-created_at",
			expectedRecords:  2,
			expectedTotal:    10,
			expectedLastPage: 5,
		},
		{
			name:             "page",
			page:             2,
			pageSize:         5,
			sort:             "-created_at",
			expectedRecords:  5,
			expectedTotal:    10,
			expectedLastPage: 2,
		},
		{
			name:             "sort by question asc",
			page:             1,
			pageSize:         20,
			sort:             "question",
			expectedRecords:  10,
			expectedTotal:    10,
			expectedLastPage: 1,
		},
		{
			name:             "sort by question desc",
			page:             1,
			pageSize:         20,
			sort:             "-question",
			expectedRecords:  10,
			expectedTotal:    10,
			expectedLastPage: 1,
		},
		{
			name:             "sort by created asc",
			page:             1,
			pageSize:         20,
			sort:             "created_at",
			expectedRecords:  10,
			expectedTotal:    10,
			expectedLastPage: 1,
		},
		{
			name:             "search",
			search:           "d",
			page:             1,
			pageSize:         20,
			sort:             "-created_at",
			expectedRecords:  1,
			expectedTotal:    1,
			expectedLastPage: 1,
		},
		{
			name:             "no matches",
			search:           "test",
			page:             1,
			pageSize:         20,
			sort:             "-created_at",
			expectedRecords:  0,
			expectedTotal:    0,
			expectedLastPage: 0,
		},
		{
			name:             "page value too high",
			page:             42,
			pageSize:         20,
			sort:             "-created_at",
			expectedRecords:  0,
			expectedTotal:    0,
			expectedLastPage: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			polls, metadata, err := testModels.Polls.GetAll(test.search, Filters{
				Page:         test.page,
				PageSize:     test.pageSize,
				Sort:         test.sort,
				SortSafelist: []string{"created_at", "question", "-created_at", "-question"},
			})
			if err != nil {
				t.Errorf("get all polls returned an error: %s", err)
			}

			if len(polls) != test.expectedRecords {
				t.Errorf("expected to get %d records but got %d", test.expectedRecords, len(polls))
			}

			if metadata.TotalRecords != test.expectedTotal {
				t.Errorf(
					"expected total records in Metadata to be %d records but got %d",
					test.expectedTotal,
					metadata.TotalRecords,
				)
			}
			if metadata.LastPage != test.expectedLastPage {
				t.Errorf(
					"expected last page in Metadata to be %d records but got %d",
					test.expectedLastPage,
					metadata.LastPage,
				)
			}

			if test.search == "" {
				switch test.sort {
				case "-created_at":
					if metadata.CurrentPage == 1 {
						if polls[0].Question != "j question" {
							t.Errorf("sorting: expected first poll to be the last one iserted but got, %q", polls[0].Question)
						}

						if polls[0].Options[0].Value != "Option one, poll j" {
							t.Errorf("options: expected option to be in first poll but got, %q", polls[0].Options[0].Value)
						}
					}
				case "created_at":
					if metadata.CurrentPage == 1 {
						if polls[9].Question != "j question" {
							t.Errorf("sorting: expected last poll to be the last one iserted but got, %q", polls[0].Question)
						}

						if polls[9].Options[0].Value != "Option one, poll j" {
							t.Errorf("options: expected option to be in first poll but got, %q", polls[9].Options[0].Value)
						}
					}
				case "question":
					if metadata.CurrentPage == 1 {
						if polls[0].Question != "a question" {
							t.Errorf("sorting by question: expected first poll question to start witn 'a', but got %q", polls[0].Question)
						}
						if polls[9].Question != "j question" {
							t.Errorf("sorting by question: expected last poll question to start witn 'j', but got %q", polls[9].Question)
						}
					}
				case "-question":
					if metadata.CurrentPage == 1 {
						if polls[0].Question != "j question" {
							t.Errorf("sorting by question: expected first poll question to start witn 'j', but got %q", polls[0].Question)
						}
						if polls[9].Question != "a question" {
							t.Errorf("sorting by question: expected last poll question to start witn 'a', but got %q", polls[9].Question)
						}
					}
				default:
					t.Fatal("unknown sort value")
				}
			}

			if test.search != "" && test.expectedRecords != 0 {
				if !strings.Contains(polls[0].Question, test.search) {
					t.Errorf("expected found poll question to contain %q but got %q", test.search, polls[0].Question)
				}
				opt := fmt.Sprintf("Option one, poll %s", test.search)
				if polls[0].Options[0].Value != opt {
					t.Errorf("expected option %q to be in poll but got, %q", opt, polls[0].Options[0].Value)
				}
			}
		})
	}
}
