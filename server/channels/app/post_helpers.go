// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"
	"sort"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/request"
)

type filterPostOptions struct {
	assumeSortedCreatedAt bool
}

type accessibleBounds struct {
	start int
	end   int
}

func (b accessibleBounds) allAccessible(lenPosts int) bool {
	return b.start == allAccessibleBounds(lenPosts).start && b.end == allAccessibleBounds(lenPosts).end
}

func (b accessibleBounds) noAccessible() bool {
	return b.start == noAccessibleBounds.start && b.end == noAccessibleBounds.end
}

// assumes checking was already performed that at least one post is inaccessible
func (b accessibleBounds) getInaccessibleRange(listLength int) (int, int) {
	var start, end int
	if b.start == 0 {
		start = b.end + 1
		end = listLength - 1
	} else {
		start = 0
		end = b.start - 1
	}
	return start, end
}

var noAccessibleBounds = accessibleBounds{start: -1, end: -1}
var allAccessibleBounds = func(lenPosts int) accessibleBounds { return accessibleBounds{start: 0, end: lenPosts - 1} }

// getTimeSortedPostAccessibleBounds returns what the boundaries are for accessible posts.
// It assumes that CreateAt time for posts is monotonically increasing or decreasing.
// It could be either because posts can be returned in ascending or descending time order.
// Special values (which can be checked with methods `allAccessible` and `allInaccessible`)
// denote if all or none of the posts are accessible.
func getTimeSortedPostAccessibleBounds(earliestAccessibleTime int64, lenPosts int, getCreateAt func(int) int64) accessibleBounds {
	if lenPosts == 0 {
		return allAccessibleBounds(lenPosts)
	}
	if lenPosts == 1 {
		if getCreateAt(0) >= earliestAccessibleTime {
			return allAccessibleBounds(lenPosts)
		}
		return noAccessibleBounds
	}

	ascending := getCreateAt(0) < getCreateAt(lenPosts-1)

	idx := sort.Search(lenPosts, func(i int) bool {
		if ascending {
			// Ascending order automatically picks the left most post(at idx),
			// in case multiple posts at idx, idx+1, idx+2... have the same time.
			return getCreateAt(i) >= earliestAccessibleTime
		}
		// Special case(subtracting 1) for descending order to include the right most post(at idx+k),
		// in case multiple posts at idx, idx+1, idx+2...idx+k have the same time.
		return getCreateAt(i) <= earliestAccessibleTime-1
	})

	if ascending {
		if idx == lenPosts {
			return noAccessibleBounds
		}
		return accessibleBounds{start: idx, end: lenPosts - 1}
	}

	if idx == 0 {
		return noAccessibleBounds
	}
	return accessibleBounds{start: 0, end: idx - 1}
}

// linearFilterPostList make no assumptions about ordering, go through posts one by one
// this is the slower fallback that is still safe if we can not
// assume posts are ordered by CreatedAt
func linearFilterPostList(postList *model.PostList, earliestAccessibleTime int64) {
	// filter Posts
	posts := postList.Posts
	order := postList.Order

	n := 0
	for i, postID := range order {
		if createAt := posts[postID].CreateAt; createAt >= earliestAccessibleTime {
			order[n] = order[i]
			n++
		} else {
			if createAt > postList.FirstInaccessiblePostTime {
				postList.FirstInaccessiblePostTime = createAt
			}
			delete(posts, postID)
		}
	}
	postList.Order = order[:n]

	// it can happen that some post list results don't have all posts in the Order field.
	// for example GetPosts in the CollapsedThreads = false path, parents are not added
	// to Order
	for postId := range posts {
		if createAt := posts[postId].CreateAt; createAt < earliestAccessibleTime {
			if createAt > postList.FirstInaccessiblePostTime {
				postList.FirstInaccessiblePostTime = createAt
			}
			delete(posts, postId)
		}
	}
}

// linearFilterPostsSlice make no assumptions about ordering, go through posts one by one
// this is the slower fallback that is still safe if we can not
// assume posts are ordered by CreatedAt
func linearFilterPostsSlice(posts []*model.Post, earliestAccessibleTime int64) ([]*model.Post, int64) {
	var firstInaccessiblePostTime int64
	n := 0
	for i := range posts {
		if createAt := posts[i].CreateAt; createAt >= earliestAccessibleTime {
			posts[n] = posts[i]
			n++
		} else {
			if createAt > firstInaccessiblePostTime {
				firstInaccessiblePostTime = createAt
			}
		}
	}
	return posts[:n], firstInaccessiblePostTime
}

// filterInaccessiblePosts - post history filtering disabled by patch
func (a *App) filterInaccessiblePosts(postList *model.PostList, options filterPostOptions) *model.AppError {
	return nil
}

// isInaccessiblePost indicates if the post is past the cloud plan's limit.
func (a *App) isInaccessiblePost(post *model.Post) (int64, *model.AppError) {
	if post == nil {
		return 0, nil
	}

	pl := &model.PostList{
		Order: []string{post.Id},
		Posts: map[string]*model.Post{post.Id: post},
	}

	return pl.FirstInaccessiblePostTime, a.filterInaccessiblePosts(pl, filterPostOptions{assumeSortedCreatedAt: true})
}

// getFilteredAccessiblePosts - post history filtering disabled by patch
func (a *App) getFilteredAccessiblePosts(posts []*model.Post, options filterPostOptions) ([]*model.Post, int64, *model.AppError) {
	return posts, 0, nil
}

// filterBurnOnReadPosts filters out burn-on-read posts from a PostList.
// This should be used for contexts where burn-on-read posts should not appear (e.g., search results).
func (a *App) filterBurnOnReadPosts(postList *model.PostList) *model.AppError {
	if postList == nil || postList.Posts == nil || len(postList.Posts) == 0 {
		return nil
	}

	// Check if burn-on-read feature is enabled
	if !a.Config().FeatureFlags.BurnOnRead || !model.SafeDereference(a.Config().ServiceSettings.EnableBurnOnRead) {
		// Feature is not enabled, no need to filter
		return nil
	}

	// Collect burn-on-read post IDs
	var burnOnReadPostIDs []string
	for postID, post := range postList.Posts {
		if post.Type == model.PostTypeBurnOnRead {
			burnOnReadPostIDs = append(burnOnReadPostIDs, postID)
		}
	}

	// If no burn-on-read posts found, nothing to filter
	if len(burnOnReadPostIDs) == 0 {
		return nil
	}

	// Remove burn-on-read posts from the list
	for _, postID := range burnOnReadPostIDs {
		a.removePostFromList(postList, postID)
	}

	// Filter Order slice directly to ensure all burn-on-read posts are removed
	filteredOrder := make([]string, 0, len(postList.Order))
	for _, postID := range postList.Order {
		if post, exists := postList.Posts[postID]; exists && post.Type != model.PostTypeBurnOnRead {
			filteredOrder = append(filteredOrder, postID)
		}
	}
	postList.Order = filteredOrder

	// Clear BurnOnReadPosts map as burn-on-read posts should not appear
	postList.BurnOnReadPosts = make(map[string]*model.Post)

	// Update NextPostId and PrevPostId if they point to removed posts
	if postList.NextPostId != "" {
		if _, exists := postList.Posts[postList.NextPostId]; !exists {
			postList.NextPostId = ""
		}
	}
	if postList.PrevPostId != "" {
		if _, exists := postList.Posts[postList.PrevPostId]; !exists {
			postList.PrevPostId = ""
		}
	}

	return nil
}

// revealSingleBurnOnReadPost reveals a single burn-on-read post for a user.
// If the post is not a burn-on-read post, it returns the post unchanged.
// If the post is expired or inaccessible, it returns an error.
func (a *App) revealSingleBurnOnReadPost(rctx request.CTX, post *model.Post, userID string) (*model.Post, *model.AppError) {
	if post == nil {
		return nil, model.NewAppError("revealSingleBurnOnReadPost", "app.post.get.app_error", nil, "", http.StatusBadRequest)
	}

	// If not a burn-on-read post, return as-is
	if post.Type != model.PostTypeBurnOnRead {
		return post, nil
	}

	// Check if burn-on-read feature is enabled
	if !a.Config().FeatureFlags.BurnOnRead || !model.SafeDereference(a.Config().ServiceSettings.EnableBurnOnRead) {
		// Feature is not enabled, return post as-is
		return post, nil
	}

	tmpPostList := model.NewPostList()
	tmpPostList.AddPost(post)

	postList, appErr := a.revealBurnOnReadPostsForUser(rctx, tmpPostList, userID)
	if appErr != nil {
		return nil, appErr
	}

	revealedPost, ok := postList.Posts[post.Id]
	if !ok {
		return nil, model.NewAppError("revealSingleBurnOnReadPost", "app.post.get.app_error", nil, "", http.StatusNotFound)
	}

	return revealedPost, nil
}

// revealBurnOnReadPostsForUser processes burn-on-read posts in a post list for a specific user,
// revealing posts that the user has access to and handling expired receipts.
func (a *App) revealBurnOnReadPostsForUser(rctx request.CTX, postList *model.PostList, userID string) (*model.PostList, *model.AppError) {
	if postList == nil || postList.BurnOnReadPosts == nil || len(postList.BurnOnReadPosts) == 0 {
		return postList, nil
	}

	// Check if burn-on-read feature is enabled
	if !a.Config().FeatureFlags.BurnOnRead || !model.SafeDereference(a.Config().ServiceSettings.EnableBurnOnRead) {
		// Feature is not enabled, return postList as-is
		return postList, nil
	}

	for _, post := range postList.BurnOnReadPosts {
		if post.DeleteAt > 0 {
			continue
		}

		// If user is the author, reveal the post with recipients
		if post.UserId == userID {
			if err := a.revealPostForAuthor(rctx, postList, post); err != nil {
				return nil, err
			}
			continue
		}

		// Get user's read receipt for this post
		receipt, err := a.getUserReadReceipt(rctx, post.Id, userID)
		if err != nil {
			return nil, err
		}

		// If no receipt exists, show unrevealed message
		if receipt == nil {
			a.setUnrevealedPost(postList, post.Id)
			continue
		}

		// If receipt expired, remove post from list
		if a.isReceiptExpired(receipt) {
			a.removePostFromList(postList, post.Id)
			continue
		}

		// Reveal post with expiration metadata
		if err := a.revealPostForUser(rctx, postList, post, receipt); err != nil {
			return nil, err
		}
	}

	return postList, nil
}
