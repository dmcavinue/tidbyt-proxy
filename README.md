
# setup
Copy `.env.example` and fill in your tidbyt api key and device id into the respective env vars.
`./templates/notify.star` is used to render a pixlet go template and push to the target device.
An included postman collection is included at `apps\tidbyt-proxy\postman.json` for testing of templates.

# Example Home Assistant `rest_command` Service:

```yaml
rest_command:
  tidbyt-notify:
    url: http://tidbyt:8080/api/notify
    payload: '{"text": "{{ text }}", "textcolor": "{{ textcolor }}", "bgcolor": "{{ bgcolor }}", "icon": "{{ icon }}"}'
    method: POST
```

# testing templates with curl:
```
docker-compose up --build
curl -k http://localhost:8080/api/notify -d '{"text": "this is a test", "textcolor": "#000000", "bgcolor": "#ffffff", "icon": "parrot", "returnimage", true}'
```

# comments

Slack icon code mostly taken from [here](https://github.com/tidbyt/community/tree/main/apps/randomslackmoji) with thanks to [btjones](https://github.com/btjones/)