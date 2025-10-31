## Description

<!-- Provide a clear and concise description of the changes -->

## Type of Change

- [ ] 🐛 Bug fix (non-breaking change which fixes an issue)
- [ ] ✨ New feature (non-breaking change which adds functionality)
- [ ] 💥 Breaking change (fix or feature that would cause existing functionality to not work as expected)
- [ ] 📚 Documentation update
- [ ] 🔧 Configuration change
- [ ] ♻️ Code refactoring (no functional changes)
- [ ] ⚡ Performance improvement
- [ ] ✅ Test addition or improvement

## Related Issue

<!-- Link to the issue this PR addresses, if applicable -->
Closes #

## Changes Made

<!-- List the specific changes made in this PR -->

- 
- 
- 

## Testing Performed

<!-- Describe the tests you ran to verify your changes -->

- [ ] Ran `go test ./...`
- [ ] Ran `go test -race ./...`
- [ ] Tested with `--verbose` flag
- [ ] Tested with a real organization
- [ ] Tested resume functionality (if applicable)
- [ ] Tested with feature flags (if applicable)

### Test Commands
```bash
# Add the specific commands you used for testing
gh stats run --org test-org --verbose
```

## Code Quality Checklist

- [ ] Code follows the project's style guidelines (`go fmt`)
- [ ] Ran `go vet ./...` with no issues
- [ ] Ran `golangci-lint run` with no issues
- [ ] Self-reviewed the code
- [ ] Added comments for complex logic
- [ ] Added godoc comments to exported functions
- [ ] Followed patterns from [Developer Guide](../DEVELOPER_GUIDE.md)

## API Usage Considerations

- [ ] All API calls use rate limiting (`client.Take()`)
- [ ] All API calls tracked (`IncrementAPICalls()`)
- [ ] Rate limiter updated from response headers
- [ ] Used `gh` CLI (not direct HTTP requests)
- [ ] Errors include org/repo context
- [ ] Non-critical errors logged as warnings

## Documentation Updates

- [ ] Updated README.md (if user-facing change)
- [ ] Updated DEVELOPER_GUIDE.md (if new pattern)
- [ ] Updated feature flags table (if applicable)
- [ ] Updated output format documentation (if changed)
- [ ] Added/updated godoc comments

## Performance Impact

<!-- Describe any performance implications -->

- Estimated API calls added per repository: 
- Rate limit impact: 
- Memory usage change: 

## Breaking Changes

<!-- If this is a breaking change, describe the impact and migration path -->

## Screenshots/Output

<!-- If applicable, add screenshots or example output -->

```json
// Example output if JSON structure changed
```

## Additional Context

<!-- Add any other context about the PR here -->

## Reviewer Notes

<!-- Specific areas you'd like reviewers to focus on -->

---

**Before submitting:**
1. Review the [Contributing Guidelines](CONTRIBUTING.md)
2. Check the [Developer Guide](../DEVELOPER_GUIDE.md)
3. Ensure all checkboxes above are completed
