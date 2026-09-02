import os
from flask import Flask

path = os.environ['PATH']
assert path.startswith("/app/.venv/bin"), f"Expected PATH to start with /app/.venv/bin but got {path}"

app = Flask(__name__)

@app.route("/")
def hello():
    return "Hello from Python Poetry!"

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=8080)
