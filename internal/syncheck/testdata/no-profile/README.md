Fixture: no go.mod at the root and no uv.lock or pyproject.toml anywhere. The
ci.yml here would fail several Go rules; workflow-sync must report only
`profile`, because it cannot know which policy applies.
