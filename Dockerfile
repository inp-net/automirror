# Use the official Python image
FROM python:3.11-slim

LABEL org.opencontainers.image.source=https://github.com/inp-net/automirror
LABEL org.opencontainers.image.description="Automatically create push-mirrors from a gitlab instance to a github organization for public repositories having a certain topic"
LABEL org.opencontainers.image.licenses="MIT"

# Set the working directory
WORKDIR /app

# Copy the current directory contents into the container at /app
COPY . /app

# Install poetry
RUN pip install poetry

# Install project dependencies
RUN poetry install

# Run the script
CMD ["poetry", "run", "python", "main.py"]
