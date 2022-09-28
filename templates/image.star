{{ define "image" }}
load("encoding/base64.star", "base64")

load("render.star", "render")

IMAGE = base64.decode("""
{{ .Image }}
""")

def main():
    
    return render.Root(
        child = render.Box(
            color = "{{ .BackgroundColor }}",
            child = render.Image(
                src = IMAGE
                height = {{ .Height }}
                width = {{ .Width }}
                delay = {{ .Delay }}
            )
        ),
    )
{{ end }}