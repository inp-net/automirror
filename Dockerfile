# Use the official Python image
FROM python:3.11-slim

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
