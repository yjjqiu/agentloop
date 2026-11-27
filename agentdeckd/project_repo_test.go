package main

import "testing"

func TestParseRepoRemote(t *testing.T) {
	cases := []struct {
		in       string
		ok       bool
		platform string
		slug     string
		url      string
	}{
		{"https://github.com/owner/repo.git", true, repoPlatformGitHub, "owner/repo", "https://github.com/owner/repo"},
		{"https://github.com/owner/repo", true, repoPlatformGitHub, "owner/repo", "https://github.com/owner/repo"},
		{"git@github.com:owner/repo.git", true, repoPlatformGitHub, "owner/repo", "https://github.com/owner/repo"},
		{"ssh://git@github.com/owner/repo.git", true, repoPlatformGitHub, "owner/repo", "https://github.com/owner/repo"},
		{"https://www.github.com/owner/repo/", true, repoPlatformGitHub, "owner/repo", "https://github.com/owner/repo"},
		{"git@atomgit.com:openatom/repo.git", true, repoPlatformAtomGit, "openatom/repo", "https://atomgit.com/openatom/repo"},
		{"https://atomgit.com/openatom/repo.git", true, repoPlatformAtomGit, "openatom/repo", "https://atomgit.com/openatom/repo"},
		{"https://gitlab.com/group/sub/repo.git", true, repoPlatformOther, "sub/repo", "https://gitlab.com/sub/repo"},
		{"", false, "", "", ""},
		{"https://github.com/owner", false, "", "", ""},
		{"not-a-remote", false, "", "", ""},
	}
	for _, c := range cases {
		repo, ok := parseRepoRemote(c.in)
		if ok != c.ok {
			t.Fatalf("parseRepoRemote(%q) ok=%v, 期望 %v", c.in, ok, c.ok)
		}
		if !ok {
			continue
		}
		if repo.Platform != c.platform || repo.Slug != c.slug || repo.URL != c.url {
			t.Fatalf("parseRepoRemote(%q) = %#v, 期望 platform=%s slug=%s url=%s", c.in, repo, c.platform, c.slug, c.url)
		}
	}
}
