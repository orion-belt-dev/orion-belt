namespace OrionBelt.SDK;

public sealed record ChallengeResponse(string Challenge);

public sealed record LoginUser(
    string Id,
    string Username,
    string Email,
    bool IsAdmin,
    string? Role = null,
    bool? MFAEnabled = null,
    bool? PasswordSet = null,
    bool? MustSetPassword = null);

public sealed record LoginResponse(
    string SessionToken,
    string ExpiresAt,
    string? AccessToken = null,
    LoginUser? User = null);

public sealed record LoginWithKeyResponse(string ApiKey, string? ExpiresAt = null, object? User = null);

public sealed record JWTLoginResponse(string AccessToken, string TokenType, string ExpiresAt, object? User = null);

public sealed record AuthMe(
    string Id,
    string Username,
    string Email,
    string PublicKey,
    bool IsAdmin,
    string Role,
    bool MFAEnabled,
    bool WebAuthnEnabled,
    bool PasswordSet,
    bool MustSetPassword,
    string? CreatedAt = null,
    string? UpdatedAt = null);

public sealed record Machine(
    string Id,
    string Name,
    string Hostname,
    int Port,
    string? AgentId = null,
    bool? IsActive = null,
    IReadOnlyDictionary<string, string>? Tags = null);

public sealed record UsageDashboard(
    int WindowHours,
    string From,
    string To,
    string GeneratedAt,
    object AccessVolume,
    object ApprovalLatency,
    IReadOnlyList<object> TopTargets);

public sealed record FileEntry(string Name, string Path, bool IsDir, long Size, long MTime);

public sealed record FileListResponse(string Path, IReadOnlyList<FileEntry> Entries, string? Raw = null);

public sealed record FileUploadResponse(string Message, string Path, int Size);