# Assrt statuses

Every Assrt answer carries `status`, 0 for success, and `errmsg` otherwise. The documentation (`https://assrt.net/api/doc`, "错误值", last modified 2020-04-27) lists these; "seen" is the 2026-11-12 capture's HTTP status for it. The adapter reads the status whatever the HTTP status, since the capture contradicts the documented 200, 4xx and 5xx split (20900 came as 200).

| Status | errmsg | Meaning | Documented as | Seen | Failure |
|---|---|---|---|---|---|
| 1 | no such user | the token's user does not exist | APIError, 200 | | Auth |
| 101 | length of keyword must be longer than 3 | the search text is too short | APIError, 200 | 200 (`status-101.json`) | Permanent |
| 20000 | your request is missing essential arguments | a required parameter is missing | ClientFail, 4xx | 400 (`status-20000.json`) | Permanent |
| 20001 | invalid token | the token does not exist | ClientFail, 4xx | 400 (`status-20001.json`) | Auth |
| 20400 | API endpoint not found | no such endpoint | ClientFail, 4xx | | Permanent |
| 20900 | subtitle not found | no subtitle has the id | ClientFail, 4xx | 200 (`status-20900.json`) | NotFound |
| 30000 | server is encounting errors | server error | ServerFail, 5xx | | Transient |
| 30001 | database is unavailable | database down | ServerFail, 5xx | | Transient |
| 30002 | search engine is unavailable | search engine down | ServerFail, 5xx | | Transient |
| 30300 | API is temporarily unavailable | API down | ServerFail, 5xx | | Transient |
| 30900 | you are exceeding request limits | the quota is spent | ServerFail, 5xx | | RateLimited for a minute, then the quota is read again |

Any other non-zero status is Permanent.
